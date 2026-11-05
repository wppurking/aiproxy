# Image API 转换

本文整理 aiproxy 对图片相关 API 的转接能力。代码路径均相对仓库根目录下的 `core/`。

## 入口

| 客户端请求 | 模式 | 代码位置 |
| --- | --- | --- |
| `POST /v1/images/generations` | `ImagesGenerations` | `router/relay.go:111` |
| `POST /v1/images/edits`（multipart） | `ImagesEdits` | `router/relay.go:107` |
| `POST /v1` / `POST /v1beta/models/*model`（Gemini 原生） | `Gemini` | `router/relay.go:57,69,81`、`controller/relay.go:810-836` |
| `POST /v1/chat/completions` | `ChatCompletions` | `router/relay.go:99` |

模式匹配规则见 `middleware/distributor.go:414-533`：

- `ImagesGenerations` 可匹配模型类型 `ImagesGenerations` / `ImagesEdits` / `GeminiImage`。
- `ImagesEdits` 可匹配 `ImagesGenerations` / `ImagesEdits`。

## `/v1/images/generations`

| 上游 | 上游目标 | 转换内容 | 代码位置 |
| --- | --- | --- | --- |
| OpenAI 及 OpenAI 兼容渠道 | `{base}/images/generations` | 重写 `model`；`gpt-image*` 删除 `response_format`；请求 `b64_json` 时把 URL 下载成 b64；SSE 原样透传 | `relay/adaptor/openai/adaptor.go:202,455,544`、`relay/adaptor/openai/image.go:25,44,168,201,252` |
| Azure OpenAI | `/openai/deployments/{model}/images/generations` | 同 OpenAI，但 URL 使用 deployment，请求体删除 `model` | `relay/adaptor/azure/main.go:32,67,93` |
| Gemini | `{base}/v1beta/models/{model}:generateContent`（stream 时 `:streamGenerateContent?alt=sse`） | `prompt`→`contents`；`size`→`imageConfig.aspectRatio` + `imageSize`（512/1K/2K/4K）；强制 `responseModalities:["IMAGE"]`；忽略 `n`；`inlineData`→`b64_json`（`response_format=url` 时给 `data:` URL） | 判定：`relay/adaptor/gemini/openai.go:118-124`、`relay/adaptor/gemini/adaptor.go:40-42`；URL：`relay/adaptor/gemini/adaptor.go:127-184`；请求：`relay/adaptor/gemini/image.go:39,136`；响应：`relay/adaptor/gemini/image.go:439,552,581` |
| Vertex AI | `projects/.../publishers/google/models/{model}:generateContent` | 复用 Gemini 转换；仅支持生成，不支持编辑 | `relay/adaptor/vertexai/adaptor.go:40-42,97-116,151-181`、`relay/adaptor/vertexai/gemini/adapter.go:30-31,124` |
| 阿里 DashScope | wanx：`/api/v1/services/aigc/text2image/image-synthesis`（异步）<br>qwen-image / wan2.6-t2i / z-image：`/multimodal-generation/generation`<br>qwen-mt-image：`/image2image/image-synthesis` | `size` 统一为 `*` 分隔；`n` 上限按模型；响应统一为 OpenAI Images | `relay/adaptor/ali/adaptor.go:80,92,226-243`、`relay/adaptor/ali/image.go:112,160,269,769,852,978,1006` |
| 豆包 / Volcengine | `{base}/api/v3/images/generations` | 重写 `model`；`size` 的 `×/*`→`x`；`n>1` 转为 `sequential_image_generation:"auto"` + `max_images`（≤15） | `relay/adaptor/doubao/main.go:92,228`、`relay/adaptor/doubao/image.go:65,97,119,150,180,247` |
| SiliconFlow | `{base}/images/generations` | `n`→`batch_size`、`steps`→`num_inference_steps`、`scale`→`guidance_scale`、`size`→`image_size`；`images[].url`→OpenAI `data[]` | `relay/adaptor/siliconflow/adaptor.go:77`、`relay/adaptor/siliconflow/image.go:44,106,126,168` |
| 百度 ERNIE | `/rpc/2.0/ai_custom/v1/wenxinworkshop/text2image/{endpoint}` | 模型名映射到 endpoint；`b64_image`→`b64_json` | `relay/adaptor/baidu/adaptor.go:65,79,154`、`relay/adaptor/baidu/image.go:27,78` |

### 通用规则

- `response_format` 只在客户端请求 `b64_json` 时做 URL→b64 下载；不做 b64→URL。
- OpenAI 兼容渠道均为透传，上游不实现对应端点时会直接返回上游错误。

## `/v1/images/edits`

| 上游 | 转换内容 | 代码位置 |
| --- | --- | --- |
| OpenAI 及 OpenAI 兼容渠道 | multipart 重新打包并重写 `model`；Azure 删除 `model` 并使用 deployment URL | `relay/adaptor/openai/image.go:80-166`、`relay/adaptor/openai/adaptor.go:212,457`、`relay/adaptor/azure/main.go:73,110` |
| Gemini | `image` / `image[]` 文件及 `image` / `image_url` 字段→Gemini `inlineData`；`data:` 直接解码，`http(s)` 默认下载为 base64（`disable_auto_image_url_to_base64` 可关闭）；`prompt` 作为文本 part；`size`→`imageConfig` | `relay/adaptor/gemini/adaptor.go:214`、`relay/adaptor/gemini/image.go:78,161-260` |
| 阿里 DashScope | qwen-image：多图转 multimodal `messages[0].content`；qwen-mt-image：走翻译接口 | `relay/adaptor/ali/adaptor.go:94,269`、`relay/adaptor/ali/image.go:190-380` |

豆包、SiliconFlow、百度、Vertex AI 不支持 `ImagesEdits`。

## Gemini 原生 `:generateContent`

| 上游 | 转换内容 | 代码位置 |
| --- | --- | --- |
| Gemini / Vertex Gemini | 原样透传，支持图片生成及带图多轮 | `relay/adaptor/gemini/adaptor.go:208`、`relay/adaptor/vertexai/gemini/adapter.go:28-29` |
| OpenAI 兼容及其他渠道 | 转成 `/chat/completions`（Responses-only 模型转 `/responses`）；`responseModalities`、`imageConfig` 会丢失，响应图片无法解析，**不支持作为生图入口** | `relay/adaptor/openai/adaptor.go:149-170,485-490`、`relay/adaptor/openai/gemini.go:28,112,515` |

## Chat 中的图片

| 方向 | 转换内容 | 代码位置 |
| --- | --- | --- |
| OpenAI `image_url` → Gemini | `data:image/...`→`inlineData`；`http(s)`→`fileData`，默认并发下载转 `inlineData` | `relay/adaptor/gemini/openai.go:417-459,833-873,1085`、`common/image/image.go:79` |
| Gemini `inlineData` / `fileData` → OpenAI | 转为 `image_url`（base64 包成 `data:` URL） | `relay/adaptor/openai/gemini.go:692-703,725-760` |
| OpenAI `image_url` → Anthropic | 转为 Claude `image.source`，URL 默认自动下载为 base64 | `relay/adaptor/anthropic/openai.go:253-262,318-332` |
| Claude 图片块 → OpenAI | 转为 `image_url` | `relay/adaptor/openai/claude.go:197-212` |
| OpenAI chat → Gemini image 模型 | 自动补 `responseModalities:["Text","Image"]`；响应 `inlineData`→`image_url`（流式同样处理） | `relay/adaptor/gemini/openai.go:224-230,990-1021,1328-1398,1483` |
| OpenAI `image_url` → Ollama | 下载 URL 转 base64 后放入 `images` | `relay/adaptor/ollama/main.go:61-82` |

## 不支持

- `POST /v1/images/variations`：返回 501（`controller/relay-controller.go:1225`）。
- Gemini 原生生图 → 非 Gemini 上游：图片语义丢失。
- `/v1/images/edits` 只接受 multipart，不接受 JSON。
- Gemini 生图的 `n` 参数会被忽略。
- `geminiopenai` 渠道虽然继承 OpenAI 的 image 模式，但 Gemini 的 `/v1beta/openai` 兼容层没有 `/images/*` 端点（`relay/adaptor/geminiopenai/adaptor.go:10-25`）。
- 百度 chat 无图片输入转换（`relay/adaptor/baidu/adaptor.go:144-170`）。
