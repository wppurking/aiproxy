# LLM API 转换

本文整理 aiproxy 在 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Gemini `generateContent` 之间的转接能力。代码路径均相对仓库根目录下的 `core/`。

> 本文只描述“请求/响应成功转换”的字段与协议行为；错误码格式、计费、日志、重试不展开。Thinking / reasoning 参数的归一化另见 `docs/REASONING_COMPATIBILITY.zh.md`。

## 入口

| 客户端请求 | 模式 | 代码位置 |
| --- | --- | --- |
| `POST /v1/chat/completions` | `ChatCompletions` | `router/relay.go:100`、`controller/relay.go:84` |
| `POST /v1/responses` | `Responses` | `router/relay.go:187`、`controller/relay.go:688` |
| `POST /v1/responses/compact` | `ResponsesCompact` | `router/relay.go:189` |
| `GET/DELETE /v1/responses/:id`、`POST /v1/responses/:id/cancel`、`GET /v1/responses/:id/input_items` | `ResponsesGet` / `ResponsesDelete` / `ResponsesCancel` / `ResponsesInputItems` | `router/relay.go:193-207`、`relay/adaptor/openai/response.go:746-830` |
| `POST /v1/messages` | `Anthropic` | `router/relay.go:104`、`controller/relay.go:58` |
| `POST /v1/completions` | `Completions` | `router/relay.go:96` |
| `POST /v1/models/*model`、`POST /v1beta/models/*model` | `Gemini` | `router/relay.go:57,69,81`、`controller/relay.go:810-836` |
| `POST /v1/alpha/search` | `AlphaSearch` | `router/relay.go:191` |

模式匹配规则见 `middleware/distributor.go:414-533`。其中 `ChatCompletions`、`Anthropic`、`Gemini` 三种请求模式可以匹配到模型类型 `ChatCompletions` / `Completions` / `Anthropic` / `Gemini` / `GeminiTTS` / `GeminiImage` / `Responses`；`Responses` 请求模式不会匹配到只支持 `Completions` 的模型。

`controller.GeminiByPath` 只区分三种情况：

- 路径包含文件操作：`GeminiFiles`
- action 为 `predictLongRunning`：`GeminiVideo`
- 其他：`Gemini`（`generateContent`）

因此 `:countTokens`、`:embedContent` 等 action 目前没有单独实现：Gemini 上游会按 `generateContent` 发送，OpenAI 上游会按 chat 请求转换（`controller/relay.go:810-833`）。

## 总览

### 客户端协议 × 上游目标协议

“转换”指代理改写请求/响应协议；“原生/透传”指上游本身支持该协议，代理只做 model 重写、参数归一化等轻量处理；“✗”指当前没有对应转换链路。

| 客户端 \ 上游 | OpenAI Chat | OpenAI Responses | Anthropic Messages | Gemini generateContent |
| --- | --- | --- | --- | --- |
| OpenAI Chat | 透传 | 转换¹ | 转换 | 转换 |
| OpenAI Responses | ✗ | 透传 | ✗ | ✗ |
| Anthropic Messages | 转换 | 转换¹ | 原生/透传² | 转换 |
| Gemini generateContent | 转换 | 转换¹ | 转换 | 原生/透传² |

- ¹ 仅当模型被识别为 Responses-only：模型名命中 `relay/adaptor/openai/constants.go:1423-1452`，或 `ModelConfig.Type` 为 `Responses*`（`relay/adaptor/openai/constants.go:1457-1497`）。
- ² 上游是 Anthropic / Gemini 原生渠道时透传；OpenAI 兼容渠道上的 `Anthropic` 模式通常是转换。

几个关键结论：

- `/v1/responses` 请求不会降级转成 `/chat/completions`。要使用 Responses 客户端，上游必须实现 `/responses`。
- 只有 `Chat/Claude/Gemini -> Responses` 的请求转换，没有 `Responses -> Chat/Claude/Gemini` 的请求转换。
- 反向只在“响应侧”存在：Chat 客户端请求 Responses 上游时，响应会转回 Chat；Claude/Gemini 同理。
- `Anthropic` 请求可以被转到 OpenAI Chat、OpenAI Responses、Anthropic Messages、Gemini；`Responses` 请求只能到 OpenAI Responses。

### 为什么 Responses 客户端不能转 Chat / Claude / Gemini 上游

1. `mode.Responses` 在上游适配器层只被 OpenAI 兼容适配器接受；`anthropic.Adaptor.SupportMode`（`relay/adaptor/anthropic/adaptor.go:37-43`）与 `gemini.Adaptor.SupportMode`（`relay/adaptor/gemini/adaptor.go:33-61`）都不包含 `mode.Responses`。渠道选择在 `controller/relay-channel.go:436-452` 会过滤掉不支持该 mode 的渠道，所以 `/v1/responses` 不会命中 Anthropic / Gemini 渠道。
2. 对 OpenAI 兼容渠道，`mode.Responses` 的 `ConvertRequest` 是 `ConvertResponseRequest`（`relay/adaptor/openai/response.go:25-68`），只重写 model、归一化 `system` role，不做协议转换；`GetRequestURL` 固定为 `/responses`（`relay/adaptor/openai/adaptor.go:89-97`）。上游没有 `/responses` 就直接报错。
3. 现有的 `ConvertResponsesToChatCompletionResponse` / `ConvertResponsesToClaudeResponse` / `ConvertResponsesToGeminiResponse` 都是“客户端是 Chat/Claude/Gemini、上游是 Responses”的响应侧转换，不是反方向。
4. 要支持反方向，至少需要新增：
   - Responses 请求 → Chat/Claude/Gemini 请求（`input` items、`instructions`、`tools`、`text.format`、`reasoning`、`max_output_tokens` 等）。
   - Chat/Claude/Gemini 的响应/SSE → Responses 的响应/SSE 事件（`output` items、`response.created` / `in_progress` / `completed` 等）。
   - 明确 Responses 独有能力的丢弃策略：`store`、`previous_response_id`、`conversation`、`background`、`include`、内置工具、encrypted reasoning 等。

另外，`ModelConfig.Type` 只参与请求模式与模型类型的匹配，不会因为模型类型写成 `Anthropic` / `Gemini` 就把 Responses 请求转成对应协议；最终上游协议由渠道类型和 adaptor 决定。

### 转换实现索引

注意：`openai/claude.go` 是“Claude 请求 → OpenAI 请求”，而 `anthropic/openai.go` 是“OpenAI Chat 请求 → Claude 请求”，文件名对应的是所在 adaptor / 主要目标，不是源协议。

| 方向 | 函数 | 代码位置 |
| --- | --- | --- |
| Chat → Responses | `ConvertChatCompletionToResponsesRequest` | `relay/adaptor/openai/chat.go:1329` |
| Responses → Chat（响应） | `ConvertResponsesToChatCompletionResponse` / `...StreamResponse` | `relay/adaptor/openai/chat.go:1480,1721` |
| Chat → Claude | `openAIConvertRequest` | `relay/adaptor/anthropic/openai.go:56` |
| Claude → Chat | `ConvertClaudeRequest` / `ConvertClaudeRequestModel` | `relay/adaptor/openai/claude.go:21,56` |
| Chat → Gemini | `convertRequest` | `relay/adaptor/gemini/openai.go:1059` |
| Gemini → Chat | `ConvertGeminiRequest` | `relay/adaptor/openai/gemini.go:28` |
| Claude → Responses | `ConvertClaudeToResponsesRequest` | `relay/adaptor/openai/claude.go:786` |
| Responses → Claude（响应） | `ConvertResponsesToClaudeResponse` / `...StreamResponse` | `relay/adaptor/openai/claude.go:853,959` |
| Gemini → Responses | `ConvertGeminiToResponsesRequest` | `relay/adaptor/openai/gemini.go:816` |
| Responses → Gemini（响应） | `ConvertResponsesToGeminiResponse` / `...StreamResponse` | `relay/adaptor/openai/gemini.go:947,1088` |
| Claude → Gemini | `convertClaudeRequest`（先经 OpenAI 结构） | `relay/adaptor/gemini/claude.go:41` |
| Gemini → Claude | `ConvertGeminiRequestToStruct` | `relay/adaptor/anthropic/gemini.go:46` |
| Chat → Claude（Vertex） | `handleChatCompletionsRequest` | `relay/adaptor/vertexai/claude/adapter.go:101` |
| Gemini → Claude（Vertex） | `handleGeminiRequest` | `relay/adaptor/vertexai/claude/adapter.go:139` |

## 渠道 / 上游行为

大多数 OpenAI 兼容渠道直接嵌入 `openai.Adaptor`，因此会继承 OpenAI 的全量 `SupportMode`：“代理支持该模式”不等于“上游一定实现该端点”。Responses 模式尤其如此，代理只透传，不会把 Responses 请求转成 Chat。

| 渠道类型 | `/v1/chat/completions` | `/v1/responses` | `/v1/messages` | Gemini 原生 | 说明 |
| --- | --- | --- | --- | --- | --- |
| OpenAI / Azure / Azure2 / GeminiOpenAI / 多数 OpenAI 兼容渠道 | 原生 chat | 透传 `/responses`（以上游实现为准） | 转 Chat 或 Responses¹ | 转 Chat | `openai/adaptor.go:35-68` |
| Anthropic | 转 Claude | ✗ | 原生 `/messages` | 转 Claude | `anthropic/adaptor.go:37-43,171-208` |
| Gemini / Vertex Gemini | 转 Gemini | ✗ | 转 Gemini | 原生 | `gemini/adaptor.go:33-61,196-230` |
| Vertex Claude | 转 Claude | ✗ | 原生 | 转 Claude | `vertexai/claude/adapter.go:19-56,101-156` |
| 阿里 DashScope | 原生 chat | 原生 `/compatible-mode/v1/responses` | 原生 Anthropic 兼容端点 | 转 Chat | `ali/adaptor.go:34-64,80-180,261-324` |
| 豆包 / Volcengine | 原生 chat | 原生 `/responses` | 转 Chat | 转 Chat | `doubao/main.go:221-246,264-313` |
| 千帆 | 原生 chat | 原生 `/responses`（白名单模型） | 转 Chat | 转 Chat | `qianfan/adaptor.go:36-181` |
| QwenCloud / TokenDance | 原生 chat | 原生 `/responses` | 转 Chat | 转 Chat | 继承 `openai.Adaptor`，见各自 `SupportMode` |
| ZhipuCoding | 原生 chat | 原生 `/api/v1/responses` | 原生 `/api/anthropic/v1/messages` | 转 Chat | `zhipucoding/adaptor.go:38-134` |
| DeepSeek | 原生 chat | 透传（上游是否实现为准） | 原生 Anthropic 兼容端点 | 转 Chat | `deepseek/adaptor.go:40-127` |
| Zhipu / Moonshot / SiliconFlow / BaiduV2 | 原生 chat | ✗ | 转 Chat | 转 Chat | 各自 `ConvertRequest` 中的 `Anthropic/Gemini` 分支 |
| MiniMax / AntLing | 原生 chat | ✗ | 原生 Anthropic 兼容端点 | 转 Chat | `minimax/adaptor.go`、`antling/adaptor.go:116-142` |
| Streamlake | 原生 chat | ✗ | 默认转 Chat；Claude Code 模型走原生代理 | 转 Chat | `streamlake/adaptor.go:36-110` |
| AWS Bedrock | 按模型委托 | ✗ | 按模型委托 | 按模型委托 | `aws/adaptor.go:28-59` |
| Cohere / Coze | 原生 chat | ✗ | ✗ | ✗ | 只支持 chat |
| Ollama | 原生 chat | ✗ | ✗ | ✗ | 不支持 Responses / Messages / Gemini；见 `ollama/adaptor.go:31-75` |

¹ OpenAI 渠道上的 `Anthropic` 请求默认转 Chat；如果模型是 Responses-only，则转 Responses。

## OpenAI Chat ↔ OpenAI Responses

触发条件：OpenAI 渠道上，模型被识别为 Responses-only。转换入口在 `relay/adaptor/openai/adaptor.go:443-447`（`ChatCompletions`）以及 `:449-455`（`Anthropic`）、`:485-491`（`Gemini`）。

### Chat 请求 → Responses 请求

实现：`relay/adaptor/openai/chat.go:1329-1478`。

| Chat 字段 | Responses 字段 | 说明 |
| --- | --- | --- |
| `model` | `model` | 改写为实际模型 |
| `messages` | `input` | 按消息类型拆成 `message` / `function_call` / `function_call_output` |
| `stream` | `stream` | |
| `max_tokens` / `max_completion_tokens` | `max_output_tokens` | 优先 `max_tokens` |
| `temperature` / `top_p` | 同名 | |
| `response_format` | `text.format` | `json_schema` / `json_object` / `text` |
| `verbosity` | `text.verbosity` | |
| `logprobs` / `top_logprobs` | `include` / `top_logprobs` | 追加 `message.output_text.logprobs` |
| `tools` / `functions` | `tools` | function 工具；legacy functions 也支持 |
| `tool_choice` / `function_call` | `tool_choice` | |
| `parallel_tool_calls` | `parallel_tool_calls` | |
| `reasoning_effort` | `reasoning.effort` | 见 reasoning 文档 |
| `service_tier` | `service_tier` | |
| `prompt_cache_key` / `prompt_cache_options` / `prompt_cache_retention` | 同名 | |
| `user` / `safety_identifier` / `moderation` / `metadata` | 同名 | |
| `stream_options.include_obfuscation` | `stream_options.include_obfuscation` | |

消息转换规则（`ConvertMessagesToInputItems`，`relay/adaptor/openai/chat.go:1222-1327`）：

- `system` → `developer`
- `tool` + `tool_call_id` → `function_call_output`
- assistant 的 `tool_calls` → 独立的 `function_call` item
- 普通文本 → `input_text`（assistant 为 `output_text`）
- `image_url` → `input_image`，保留 `detail` 和 `prompt_cache_breakpoint`
- 其他内容类型（audio / video / file 等）不转换

### Responses 响应 → Chat 响应

实现：`relay/adaptor/openai/chat.go:1480-1639`（非流式）、`:1721-1878`（流式）。

| Responses | Chat | 说明 |
| --- | --- | --- |
| message 的 `output_text` | `choices[].message.content` | 多个 text part 用 `\n` 连接 |
| `refusal` | `message.refusal` | |
| `annotations` | `message.annotations` | |
| `output_text` 的 logprobs | `choice.logprobs.content` | |
| reasoning 的 `summary` | `reasoning_content` | 只取 `summary`，不取原始 `reasoning_text` |
| `function_call` | `tool_calls` | |
| `status` / `incomplete_details` | `finish_reason` | `completed`→`stop`；`max_output_tokens`→`length`；`content_filter`→`content_filter` |
| usage | usage | 含 cached / reasoning 明细 |
| `service_tier` | `service_tier` | |
| `id` / `created_at` / `model` | 同名 | `model` 使用客户端原始模型名 |

### 限制

- `n > 1` 直接返回 400（`relay/adaptor/openai/chat.go:1341-1346`）。
- `stop`、`frequency_penalty`、`presence_penalty`、`logit_bias`、`seed`、`audio`、`modalities` 等不映射。
- 历史消息中的 `reasoning_content` 不会转成 Responses 的 reasoning item。
- Responses 输出中的图片、音频、文件不会转成 Chat content part。
- 流式 Chat 只消费 `response.reasoning_summary_text.delta`，不消费 `response.reasoning_text.delta`。
- Responses 客户端不能反向使用 Chat-only 上游。

## OpenAI Chat ↔ Anthropic Messages

这是两组不同实现，务必区分方向。

### Chat 请求 → Claude 请求

实现：`relay/adaptor/anthropic/openai.go:56-295`；Vertex Claude 复用 `handleChatCompletionsRequest`（`relay/adaptor/vertexai/claude/adapter.go:101-116`）。

| Chat | Claude | 说明 |
| --- | --- | --- |
| `system` role | `system` text block | 多条 system 会追加为多个 block |
| `user` / `assistant` | `messages[].role` | |
| text | text block | |
| `image_url` | `image.source` | `data:` 直接转 base64；`http(s)` 默认下载成 base64，可用 `disable_auto_image_url_to_base64` 关闭 |
| `tool` role + `tool_call_id` | `user` + `tool_result` | |
| assistant `tool_calls` | `tool_use` | |
| `tools[].function` | `tools[].input_schema` | 只保留 `type` / `properties` / `required` |
| 非 function 工具 | 按同名字段透传 | server tool 语义不转换 |
| `tool_choice` | `auto` / `any` / `tool` | 见下方限制 |
| `max_tokens` | `max_tokens` | 0 时按模型给默认值 |
| `temperature` / `top_p` | 同名 | 两者同时存在时丢 `top_p` |
| `top_k` | `top_k` | OpenAI 扩展字段 |
| `reasoning_effort` | `thinking` / `output_config` | 见 reasoning 文档 |
| `stream` | `stream` | |
| message / tool 的 `cache_control` | `cache_control` | TTL 会重置 |
| `stop` / `response_format` / `n` / `seed` / penalties / logprobs / user / metadata | — | 不映射 |
| `max_completion_tokens` | — | 只读取 `max_tokens` |
| audio / video content | — | 会产生空 content block，属于已知缺口 |
| file content | — | 静默丢弃 |

限制：

- `tool_choice` 只识别 `any` 和 `{"type":"function","function":{"name":...}}`；`required` 和 `none` 都会落到 `auto`（`relay/adaptor/anthropic/openai.go:179-195`）。
- 有 tool call 时会强制关闭 thinking（`relay/adaptor/anthropic/openai.go:290-292`）。
- 转换后 `stop` 不会写入 Claude 的 `stop_sequences`，即使 `ClaudeOpenAIRequest` 里有 `Stop` 字段。

### Claude 响应 → Chat 响应

实现：`relay/adaptor/anthropic/openai.go:491-579`（非流式）、`:580-703`（流式）、`:704-746`（Handler）。

| Claude | Chat | 说明 |
| --- | --- | --- |
| `text` block | `message.content` | 非流式只保留最后一个 text block |
| `thinking` block | `reasoning_content` + `signature` | 非流式只保留最后一个 thinking block |
| `tool_use` block | `tool_calls` | |
| `stop_reason` | `finish_reason` | |
| usage | usage | 含 cache read / cache creation |
| `server_tool_use.web_search_requests` | usage 的 web search count | |
| `web_search_tool_result` / `code_execution_tool_result` | — | 忽略 |

### Claude 请求 → Chat 请求

实现：`relay/adaptor/openai/claude.go:21-341`。

| Claude | Chat | 说明 |
| --- | --- | --- |
| `system` text blocks | 一条 system 消息 | 多个 block 用 `\n` 连接，只保留 text |
| `messages` | `messages` | |
| text | text | |
| `thinking` block | text | 会退化成普通文本 |
| `image.source` | `image_url` | URL 或 base64 → `data:` URL |
| `tool_use` | assistant `tool_calls` | `signature` 会写入 `extra_content.google.thought_signature` |
| `tool_result` | `tool` 消息 | |
| `tools[].input_schema` | `function.parameters` | 只保留 `type` / `properties` / `required` |
| `tool_choice` | `auto` / `required` / function | `any`→`required` |
| `max_tokens` / `max_completion_tokens` | 同名 | |
| `temperature` / `top_p` | 同名 | |
| `stop_sequences` | `stop` | |
| `thinking` / `output_config` | `reasoning_effort` | 见 reasoning 文档 |
| `stream` | `stream` + `stream_options.include_usage` | |
| `top_k` | — | 不映射 |
| `metadata` / `cache_control` / `context_management` | — | 不映射 |

### Chat 响应 → Claude 响应

实现：`relay/adaptor/openai/claude.go:596-713`（非流式）、`:343-595`（流式）。

| Chat | Claude | 说明 |
| --- | --- | --- |
| `content` string | `text` block | |
| `reasoning_content` | `thinking` block | OpenAI 不会给 Claude 风格签名字段 |
| `tool_calls` | `tool_use` | |
| `finish_reason` | `stop_reason` | |
| usage | usage | 含 cache |
| usage 的 web search count | `server_tool_use.web_search_requests` | |
| `content` 为数组（图片等） | — | 非流式只处理 string content，最终可能只剩空 text block |

### 限制

- `max_completion_tokens` 在 Chat → Claude 方向被忽略。
- `top_k` 只在 Chat → Claude 方向可能保留（作为 OpenAI 扩展字段），Claude → Chat 方向丢失。
- Claude → Chat 会丢掉 prompt caching 的 `cache_control`。
- Chat → Claude 的 audio / video part 会产生空 content block，file part 会被静默丢弃。
- Claude 非流式的多 text / 多 thinking block 只保留最后一个。
- Claude → Chat 的 thinking 历史在下一轮会变成普通 text（`convertClaudeContent` 把 `thinking` 当 text）。

## OpenAI Chat ↔ Gemini generateContent

### Chat 请求 → Gemini 请求

实现：`relay/adaptor/gemini/openai.go:1059-1120`；核心构建函数在 `:768-831`（`buildContents`）和 `:192-280`（`buildGenerationConfig`）。

| Chat | Gemini | 说明 |
| --- | --- | --- |
| system 消息 | `systemInstruction` | 只保留最后一条 system；只取 text |
| `user` / `assistant` / `tool` | `contents[].role` | assistant→`model`；tool→`user` |
| text | `parts[].text` | |
| `image_url` | `inlineData` / `fileData` | 默认把 http(s) 图片下载成 base64；可用 `disable_auto_image_url_to_base64` 关闭 |
| `input_audio` | `inlineData` | 默认下载；可用 `disable_auto_audio_url_to_base64` 关闭 |
| `video_url` | `fileData` / `inlineData` | 默认下载；可用 `disable_auto_video_url_to_base64` 关闭 |
| assistant `tool_calls` | `functionCall` | `extra_content.google.thought_signature` → `thoughtSignature`；没有时写入占位签名 |
| `tool` 消息 | `functionResponse` | 按 `tool_call_id` 找函数名 |
| `max_tokens` | `generationConfig.maxOutputTokens` | `max_completion_tokens` 被忽略 |
| `temperature` / `top_p` | `generationConfig` | |
| `top_k` | — | 标准 OpenAI `top_k` 不映射 |
| `response_format` | `responseMimeType` / `responseSchema` | schema 会清理 Gemini 不支持的字段 |
| `reasoning_effort` | `thinkingConfig` | 见 reasoning 文档 |
| `tools[].function` | `tools[].functionDeclarations` | |
| `tool_choice` | `toolConfig.functionCallingConfig` | |
| `stop` / `n` / `seed` / penalties / logprobs / user / parallel_tool_calls | — | 不映射 |
| safety | 渠道配置 `safety` | 请求中的 safetySettings 不映射 |

限制：

- assistant 消息同时带 text 和 tool_calls 时，只输出 `functionCall`，text 会丢失（`relay/adaptor/gemini/openai.go:784-786`）。
- 多条 system 消息只保留最后一条。
- `developer` role 不会归一化成 `user`/`system`，可能被 Gemini 拒绝。
- `max_completion_tokens` 被忽略。
- `top_k`、`stop`、`n`、penalties、seed、logprobs 不映射。

### Gemini 响应 → Chat 响应

实现：`relay/adaptor/gemini/openai.go:1296-1395`（非流式）、`:1397-1501`（流式）。

| Gemini | Chat | 说明 |
| --- | --- | --- |
| `parts[].text` | `content` | |
| `thought: true` | `reasoning_content` + `signature` | |
| `functionCall` | `tool_calls` | 会生成新的 `call_` ID；`thoughtSignature`→`extra_content.google.thought_signature` |
| `inlineData` 图片 | `image_url` | 包成 `data:` URL |
| `inlineData` 音频 | `message.audio` | |
| `inlineData` 视频 | `video_url` | |
| `usageMetadata` | usage | 含 thoughts / cache / 图片 / 音频 / 视频 |
| `finishReason` | `finish_reason` | `STOP`→`stop`、`MAX_TOKENS`→`length` |
| grounding / citations | — | 只统计 web search 次数，不转引用 |

### Gemini 请求 → Chat 请求

实现：`relay/adaptor/openai/gemini.go:28-110`。

| Gemini | Chat | 说明 |
| --- | --- | --- |
| `systemInstruction` 的 text parts | 一条 system 消息 | 所有 text 拼接 |
| `contents` | messages | `model`→assistant；`user`→user |
| `parts[].text` | content | 包括 `thought: true` 的文本，统一变普通 content |
| `functionCall` | assistant `tool_calls` | 生成 `call_` ID；`thoughtSignature` 不保留 |
| `functionResponse` | tool 消息 | 按函数名匹配 call ID；找不到时补 synthetic assistant tool_call |
| `inlineData` | image_url / input_audio / video_url | 按 MIME 判断 |
| `fileData` | image_url / input_audio / video_url | 按 MIME 或文件扩展名判断 |
| `generationConfig.temperature` / `topP` / `maxOutputTokens` | temperature / top_p / max_tokens | |
| `responseMimeType` / `responseSchema` | `response_format` | |
| `thinkingConfig` | `reasoning_effort` | |
| `tools[].functionDeclarations` | `tools` | 支持 `parametersJsonSchema` |
| `toolConfig` | `tool_choice` | `AUTO`→auto，`NONE`→none，`ANY`→required 或指定函数 |
| `stopSequences` / `topK` / `candidateCount` / `safetySettings` | — | 不映射 |

### Chat 响应 → Gemini 响应

实现：`relay/adaptor/openai/gemini.go:112-191`（非流式）、`:193-238`（流式）。

| Chat | Gemini | 说明 |
| --- | --- | --- |
| `content` string | `parts[].text` | |
| `tool_calls` | `functionCall` | |
| `finish_reason` | `finishReason` | |
| usage | `usageMetadata` | |
| `reasoning_content` | — | 不映射为 `thought` |
| `content` 数组中的图片 / 音频 | — | 不映射 |

限制：

- Chat → Gemini 的响应转换会丢 reasoning（不会变成 `thought`）和图片/音频输出。
- Gemini → Chat 的请求转换会把 `thought` 文本转成普通 content，并丢失 `thoughtSignature`；只有 Gemini 响应 → Chat 响应会把 `functionCall.thoughtSignature` 写入 `extra_content.google.thought_signature`。
- Gemini 的 `fileData` 在转 Claude 时会被丢弃；转 Chat 时按 MIME 当图片/音频/视频处理。
- Gemini 的 `candidateCount`、`topK`、`stopSequences`、safety 在双向转换中都可能丢失。

## Anthropic Messages ↔ Gemini generateContent

### Claude 请求 → Gemini 请求

实现：`relay/adaptor/gemini/claude.go:41-97`。它先调用 `openai.ConvertClaudeRequestModel`（`relay/adaptor/openai/claude.go:56`）转成 OpenAI 结构，再走 Gemini 的 `buildContents` / `buildGenerationConfig`。

因此损失是两段损失的并集：

- Claude → OpenAI 阶段：`top_k`、`metadata`、`cache_control`、`thinking` 类型、`output_config.format` 丢失；`thinking` 变普通 text。
- OpenAI → Gemini 阶段：assistant text + tool_calls 时 text 丢失；`max_completion_tokens` 丢失；`stop`、`n`、penalties 等丢失。
- Claude 的 `tool_use.signature` 会通过 `extra_content.google.thought_signature` 保留到 Gemini 的 `functionCall.thoughtSignature`。
- Claude 的 `system` 多个 text block 会先合并成一条 system 消息，再进入 `systemInstruction`。
- Claude 的 `image` 会经 `image_url` → `inlineData`/`fileData`。
- Claude 的 `stop_sequences` 在 OpenAI 阶段会写入 `stop`，但 Gemini 阶段不读取 `stop`，最终丢失。

响应侧使用 `anthropic/gemini.go:141-212` 的 `ConvertClaudeToGeminiResponse`：text→text、thinking→thought、tool_use→functionCall、stop_reason→finishReason。它只写 `promptTokenCount` / `candidatesTokenCount` / `totalTokenCount`，不会把 Claude 的 cache read / cache creation 或 `server_tool_use.web_search_requests` 写入 `usageMetadata`。

### Gemini 请求 → Claude 请求

实现：`relay/adaptor/anthropic/gemini.go:46-139`。

| Gemini | Claude | 说明 |
| --- | --- | --- |
| `systemInstruction` text parts | `system` text blocks | |
| `contents` | messages | `model`→assistant；`user`→user |
| `parts[].text` | text | |
| `parts[].thought` | thinking | 不带签名 |
| `functionCall` | `tool_use` | 生成确定性的 `toolu_<msg>_<part>` ID |
| `functionResponse` | `tool_result` | 按函数名匹配 call ID；找不到时降级为 text |
| `inlineData` | image（base64） | 音频/视频也会被当作 image 传入 |
| `fileData` | — | 不处理 |
| `generationConfig.temperature` / `topP` | temperature / top_p | 两者同时存在时丢 top_p |
| `generationConfig.maxOutputTokens` | max_tokens | |
| `thinkingConfig` | thinking | 见 reasoning 文档 |
| `tools[].functionDeclarations` | tools | 只保留 type / properties / required |
| `toolConfig` | tool_choice | `AUTO`→auto；`ANY`→any 或指定 tool；`NONE` 不映射（默认 auto） |
| `stopSequences` / `topK` / `candidateCount` / `responseMimeType` / `responseSchema` / safetySettings | — | 不映射 |

限制：

- Gemini `thoughtSignature` 没有映射到 Claude 的 `signature`，工具调用和 thinking 的签名都会丢。
- `fileData` 完全不处理。
- 多音频/视频 inlineData 会被当成图片。
- `toolConfig.mode = NONE` 会退化成 auto。
- `allowedFunctionNames` 有多个时只取第一个。

### Gemini 响应 → Claude 响应

实现：`relay/adaptor/gemini/claude.go:390-463`（非流式）、`:147-388`（流式）。

| Gemini | Claude | 说明 |
| --- | --- | --- |
| text | text block | |
| `thought` | thinking block + signature | |
| `functionCall` | tool_use + signature | |
| `finishReason` | stop_reason | |
| usage | usage | 映射 cache read |
| grounding | — | 不转 citations，只统计 web search |

限制：`inlineData` 图片/音频/视频不会转成 Claude content block；Gemini 的 grounding 不转 citations，只统计 web search。

## Claude / Gemini → OpenAI Responses

触发条件：OpenAI 渠道 + Responses-only 模型（`relay/adaptor/openai/adaptor.go:449-455,485-491`）。

### Claude → Responses

实现：`relay/adaptor/openai/claude.go:786-851`。流程是 Claude → OpenAI 结构，再复用 `ConvertMessagesToInputItems`。

| Claude | Responses | 说明 |
| --- | --- | --- |
| `system` | `developer` | 经 Chat 结构转换 |
| messages | input items | text / image → message / input_image；tool_use / tool_result → function_call / function_call_output |
| `max_tokens` / `max_completion_tokens` | `max_output_tokens` | |
| `temperature` / `top_p` | 同名 | |
| `tools` / `tool_choice` | `tools` / `tool_choice` | |
| `thinking` / `output_config` | `reasoning.effort` | |
| `store` | `false` | 强制非存储 |
| `stop_sequences` / `top_k` / `metadata` / `cache_control` | — | 不映射 |
| thinking block | text | 退化成普通文本 |

### Gemini → Responses

实现：`relay/adaptor/openai/gemini.go:816-945`。

| Gemini | Responses | 说明 |
| --- | --- | --- |
| `systemInstruction` | `developer` | 经 system message 转换 |
| `contents` | input items | text / image / functionCall / functionResponse |
| `generationConfig.temperature` / `topP` / `maxOutputTokens` | 同名 | |
| `thinkingConfig` | `reasoning.effort` | |
| `tools` / `toolConfig` | `tools` / `tool_choice` | |
| `store` | `false` | 强制非存储 |
| `responseMimeType` / `responseSchema` / `stopSequences` / `topK` / `candidateCount` / `safetySettings` | — | 不映射 |
| `thought` 文本 / `thoughtSignature` | — | 变成普通文本，签名丢失 |

### Responses → Claude / Gemini 响应

| 上游响应 | 转换函数 | 支持 | 丢失 |
| --- | --- | --- | --- |
| Responses → Claude 非流式 | `ConvertResponsesToClaudeResponse` | text、reasoning content（仅 `text` / `output_text`）、usage | `function_call`（非流式不处理）、图片、音频、annotations；stop_reason 固定 `end_turn` |
| Responses → Claude 流式 | `ConvertResponsesToClaudeStreamResponse` | text、`reasoning_text.delta`、function_call、usage | `reasoning_summary_text.delta`、图片、音频；stop_reason 固定 `end_turn` |
| Responses → Gemini 非流式 | `ConvertResponsesToGeminiResponse` | text、reasoning content、function_call、usage | 图片、音频、annotations；reasoning 只识别 `content` 里的 text/output_text |
| Responses → Gemini 流式 | `ConvertResponsesToGeminiStreamResponse` | text、function_call、usage | reasoning、图片、音频；finish_reason 固定 `STOP` |

> 注意：官方 Responses 的 reasoning item 通常把文本放在 `summary`（类型 `summary_text`）或 `content`（类型 `reasoning_text`）。当前 Claude/Gemini 的非流式转换主要检查 `content` 且只认 `text`/`output_text`，因此真实上游的 reasoning 可能丢失。Chat 的非流式转换使用 `summary`（`responseReasoningSummaryText`），流式使用 `reasoning_summary_text.delta`。

## 原生 / 透传路径

| 路径 | 处理 | 代码位置 |
| --- | --- | --- |
| OpenAI Chat 原生 | 重写 model；清理空 required；补 `stream_options.include_usage`；reasoning_effort 归一化 | `relay/adaptor/openai/chat.go:483-533` |
| OpenAI Responses 原生 | 重写 model；`input` 中 `system`→`developer`；reasoning.effort 归一化 | `relay/adaptor/openai/response.go:25-68` |
| OpenAI Responses compact | 重写 model，保持 opaque items 原样 | `relay/adaptor/openai/response.go:71-97` |
| Anthropic 原生 | AST 级清理：重写 model、补 max_tokens、image URL→base64、cache TTL、context_management 过滤、删除 tool examples / defer_loading、thinking 时删 temperature、temperature/top_p 二选一 | `relay/adaptor/anthropic/main.go:192-272` |
| Gemini 原生 | AST 级保真：补 thoughtSignature、补 role、Gemini 3 去掉 AUTO toolConfig、原样回传响应 | `relay/adaptor/gemini/main.go:142-190,193-295` |
| Vertex Claude 原生 | 删除 model、设置 `anthropic_version`、移除 `context_management` / tool examples、过滤 beta | `relay/adaptor/vertexai/claude/adapter.go:118-137` |
| Vertex Gemini 原生 | 复用 Gemini 原生转换，并转换视频参数 | `relay/adaptor/vertexai/gemini/adapter.go:19-56` |

## 内容类型矩阵

| 内容 | Chat → Responses | Chat → Claude | Chat → Gemini | Claude → Chat | Gemini → Chat | Claude ↔ Gemini |
| --- | --- | --- | --- | --- | --- | --- |
| text | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| 输入图片 | ✅ `input_image` | ✅ URL→base64 | ✅ inline/fileData | ✅ `image_url` | ✅ `image_url` | ✅（Gemini `fileData` 除外） |
| 输出图片 | ❌ | ❌ | ❌（Chat 响应→Gemini 不映射） | ❌ | ✅ inlineData→image_url | ❌（Gemini 响应→Claude 忽略 inlineData） |
| 输入音频 | ❌ | ❌ | ✅ inlineData | ❌ | ✅ input_audio | ❌（Gemini→Claude 当图片） |
| 输出音频 | ❌ | ❌ | ❌ | ❌ | ✅ `message.audio` | ❌ |
| 输入视频 | ❌ | ❌ | ✅ fileData/inlineData | ❌ | ✅ video_url | ❌（Gemini→Claude 当图片） |
| PDF / file | ❌ | ❌ | ❌ | ❌ | △（`fileData` 按 MIME 落到 image/audio/video；非媒体文件会变成 `image_url`） | ❌ |
| refusal | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| annotations / citations | ✅（Chat 侧映射） | ❌ | ❌ | ❌ | ❌ | ❌ |
| logprobs | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| reasoning / thinking | ✅（summary） | ✅（reasoning_effort） | ✅（thinkingConfig） | ✅（reasoning_content） | ✅（thought） | ✅（签名部分丢失） |
| tool call | ✅ | ✅ | ✅ | ✅（非流式 Responses→Claude 除外） | ✅ | ✅ |
| server tool（web search / code execution） | ❌ | ❌（按普通工具透传） | ❌ | ❌ | ❌ | ❌ |
| structured output | ✅ | ❌ | ✅ | ❌ | ✅ | Claude→Gemini ❌；Gemini→Claude ❌ |
| prompt cache | ✅（prompt_cache_* / breakpoint） | ✅（Chat→Claude 的 cache_control） | ❌ | ❌ | ❌ | ❌ |

> ✅ 表示该方向至少有一个阶段支持，不代表双向完全保真；具体以各方向的字段表为准。△ 表示只能做有损的近似转换。

## 关键丢失点汇总

1. **Responses 是单向请求转换**：只有 Chat/Claude/Gemini → Responses；`/v1/responses` 不能转给 Chat/Claude/Gemini 上游。
2. **`stop` 在跨协议转换中只有 Claude → OpenAI Chat 会映射**（`openai/claude.go:88-90`）。Chat → Claude/Gemini、Gemini → Claude/OpenAI、Responses 都不映射。
3. **`max_completion_tokens` 在 Chat → Claude / Chat → Gemini 被忽略**：这两个方向只读取 `max_tokens`。
4. **`top_k` 在跨协议转换中只在 Chat → Claude 和原生路径保留**；Claude/Gemini 之间的转换、Gemini → Chat 都会丢失。
5. **`n` / `candidateCount`**：Chat → Responses 的 `n>1` 直接报错；Chat → Claude 无 `n`；Chat → Gemini 不设置 `candidateCount`；Gemini → Chat 可以按多个 candidate 生成多个 choice，但请求侧无法要求 `n`。
6. **`developer` role 没有统一归一化**：Chat → Gemini / Chat → Claude 可能把 `developer` 原样传下去，导致上游校验失败。
7. **多条 system 消息**：Chat → Gemini 只保留最后一条；Chat → Claude 会合并；Claude → Chat 会合并成一条。
8. **assistant text + tool_calls**：Chat → Gemini 会丢 text；Chat → Claude 会保留；Chat → Responses 会拆成 message + function_call。
9. **thinking 历史**：Claude/Gemini/OpenAI 之间切换时，历史 thinking 通常退化成普通 text，无法保留签名和“思考块”语义。
10. **签名**：
    - 工具调用签名：Claude `tool_use.signature` ↔ Gemini `functionCall.thoughtSignature` 在“Claude 客户端 + Gemini 渠道”（`gemini/claude.go`）路径上通过 `extra_content.google.thought_signature` 可以部分往返；Gemini → Claude 直连路径（`anthropic/gemini.go`）不保留签名。
    - 文本 thinking 签名：Chat → Gemini、Gemini → Claude、Claude → Gemini 的文本 thinking 都不保留完整签名。
11. **非流式 Responses → Claude**：`function_call` 没有处理，工具调用会丢失；`stop_reason` 固定 `end_turn`；reasoning 只识别 `content` 中 `text`/`output_text`，真实 `reasoning_text`/`summary` 可能丢失。
12. **Responses → Gemini 流式**：不处理 reasoning；非流式 reasoning 也只识别 `text`/`output_text`。
13. **图片/音频/视频**：跨协议只在部分方向支持，具体见内容类型矩阵；Claude 的 `fileData` 和 Gemini 的 `inlineData` 音频/视频到 Claude 都是缺口。
14. **server tools / citations / web search**：只做 usage 计数或原样透传，不做跨协议语义转换；Gemini 的 grounding、Claude 的 web_search_tool_result、OpenAI Responses 的 web_search_call 都不会转成其他协议的等价结构。
15. **`input_file` / PDF**：Chat ↔ Responses 只转换 text / image；Claude/Gemini 也没有通用文件输入转换。

## 不支持 / 已知缺口

- `POST /v1/responses` → Anthropic / Gemini 上游：没有对应 `Responses` 模式。
- `GET/DELETE/CANCEL /v1/responses/:id`、`input_items`：只有 OpenAI 兼容的 Responses 上游支持，不能转成 Claude/Gemini。
- `POST /v1/responses/compact`：仅 OpenAI 兼容上游，且只重写 model。
- `POST /v1/messages` → Anthropic 原生渠道：透传，不做协议转换；上游没有 Anthropic 兼容端点时才会走 OpenAI/Gemini 转换。
- Gemini 原生 `:countTokens` / `:embedContent`：没有路由级识别，会落到 `generateContent`。
- Gemini 原生图片生成 → 非 Gemini 上游：图片语义会丢失（见 `docs/converts/image.md`）。
- Gemini `fileData` → Claude：完全不处理。
- Claude `audio` / `video` / `file` content → 任意方向：不支持。
- OpenAI Chat 的 audio / video / file content → Claude / Responses：不支持；→ Gemini 支持 audio/video。
- OpenAI Responses 的输出图片/音频/文件 → Chat/Claude/Gemini：不支持。
- OpenAI `web_search_options`、Responses 内置工具（web_search / file_search / image_generation / computer_use / mcp）→ Claude/Gemini：不转换。
- Claude 的 `context_management`、`output_config.format`、`metadata`、`cache_control` → OpenAI/Gemini：不保留（原生 Claude 渠道除外）。
- Gemini 的 `safetySettings`、`cachedContent`、`responseSchema`（转 Claude）、`stopSequences`、`topK`、`candidateCount`：跨协议转换不保留（原生渠道除外）。

## 参考

- Thinking / reasoning 参数兼容：`docs/REASONING_COMPATIBILITY.zh.md`
- Image API 转换：`docs/converts/image.md`
- OpenAI Chat → Responses 集成测试：`core/relay/adaptor/openai/integration_test.go`
- Claude ↔ Responses 测试：`core/relay/adaptor/openai/claude_test.go`
- Gemini ↔ Responses 测试：`core/relay/adaptor/openai/gemini_to_responses_test.go`
