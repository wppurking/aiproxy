# Model Type 与 Channel Provider

本文说明 aiproxy 中 `ModelConfig.Type`（Model Type）与 `Channel.Type`（Channel Provider）的区别、请求路由顺序，以及两者如何共同决定协议转换。代码路径均相对仓库根目录下的 `core/`。

## 一句话结论

- **Model Type 是“这个模型对客户端暴露成什么 API / 能力”的标签**，用于端点兼容性校验、Responses-only 判断、Gemini Image/TTS 识别、渠道测试、流超时等。
- **Channel Provider 才是“用什么 adaptor 访问哪个上游协议”**，决定 URL、鉴权、请求/响应转换、支持哪些请求模式。
- 两者是**正交**的：Model Type 不直接选择上游协议，Provider 也不决定模型类型。

## 两套概念

| 维度 | Model Type | Channel Provider |
| --- | --- | --- |
| 字段 | `ModelConfig.Type` | `Channel.Type` |
| 类型 | `mode.Mode` | `model.ChannelType` |
| 定义 | `model/modelconfig.go:29-55`、`relay/mode/define.go` | `model/channel.go:33-63`、`model/chtype.go` |
| 回答的问题 | 这个模型对客户端是什么 API / 能力？ | 这个渠道用什么 adaptor 访问哪个上游？ |
| 决定 | 端点兼容性、Responses-only、Gemini Image/TTS、测试请求、流超时、UI 展示 | adaptor、URL、鉴权、协议转换、支持模式、默认模型/映射、渠道配置 schema |
| 不直接决定 | 上游协议、URL、鉴权（Responses-only 会间接触发转换） | 客户端模型类型、模型价格/限流配置 |

### Model Type

`ModelConfig.Type` 是一个 `mode.Mode`，常见取值：

- Chat 类：`ChatCompletions`、`Completions`、`Anthropic`、`Gemini`
- OpenAI Responses：`Responses`、`ResponsesGet`、`ResponsesDelete`、`ResponsesCancel`、`ResponsesInputItems`、`ResponsesCompact`
- 向量：`Embeddings`
- 图片：`ImagesGenerations`、`ImagesEdits`、`GeminiImage`
- 音频：`AudioSpeech`、`AudioTranscription`、`AudioTranslation`、`GeminiTTS`
- 视频：`VideoGenerationsJobs`、`Videos`、`GeminiVideo`、`AliVideo`、`DoubaoVideo` 等
- 其他：`Rerank`、`Moderations`、`ParsePdf`
- `Unknown`：未设置/未知

注意：**Model Type 不是“厂商”字段**。内置模型目录里的 Claude、Gemini 文本模型大多标成 `ChatCompletions`，而不是 `Anthropic` / `Gemini`：

- Claude：`relay/adaptor/anthropic/constants.go:10-11` 等
- Gemini 文本：`relay/adaptor/gemini/constants.go:13-14` 等
- Gemini 图片/TTS/视频才使用 `GeminiImage` / `GeminiTTS` / `GeminiVideo`：`relay/adaptor/gemini/constants.go:143-144,282-283,330-331`

这说明 Model Type 是“模型对客户端暴露的主 API / 能力标签”，不是上游协议绑定。`Type = Anthropic` / `Gemini` 更多用于渠道测试、UI 语义，或确实主要按原生 API 暴露的模型；它并不会禁用 Chat 入口（`CheckRelayMode` 仍允许 Chat 请求使用 `Anthropic` / `Gemini` 类型的模型）。

### Channel Provider

`Channel.Type` 是 `model.ChannelType`，例如：

- `openai`、`azure`、`azure2`
- `anthropic`
- `google gemini`、`google gemini (openai)`
- `vertexai`
- `ali`、`doubao`、`qianfan`、`qwencloud`、`zhipucoding`
- `deepseek`、`minimax`、`antling`、`siliconflow`、`moonshot`、`zhipu`
- `ollama`、`cohere`、`coze`
- `aws`、`cloudflare`、`openrouter`、`novita`、`groq`、`mistral`、`xai` 等

完整列表见 `model/chtype.go`。

Provider 不是“单一协议”。同一个 provider 可能支持多个原生端点或兼容端点，例如：

- `ali`：OpenAI 兼容 chat、Responses、原生 Anthropic 兼容端点、Gemini 请求转换
- `deepseek`：OpenAI 兼容 chat、原生 Anthropic 兼容端点
- `zhipucoding`：OpenAI 兼容 chat、原生 Anthropic、原生 Responses
- `vertexai`：按模型名选择 Gemini 或 Claude publisher

因此，**Provider 是 adaptor 家族，不是协议枚举**。

## 请求路由流程

```text
请求 URL
  │
  ├─ 决定 requestMode：ChatCompletions / Responses / Anthropic / Gemini / Embeddings / Images ...
  │
  ├─ 根据 model 名取 ModelConfig
  │
  ├─ CheckRelayMode(requestMode, ModelConfig.Type)     ← 第一道门：Model Type 是否允许这个端点
  │
  ├─ 找 channel.Models 包含该 model 的渠道
  │
  ├─ 对每个渠道：
  │     provider = channel.Type
  │     adaptor = registry[provider]
  │     adaptor.SupportMode(meta)                      ← 第二道门：Provider 是否支持这个 requestMode
  │
  ├─ 选渠道（优先级 / 权重 / 错误率）
  │
  └─ adaptor.ConvertRequest() / DoResponse()
        → 决定真正发往上游的协议：/chat/completions、/responses、/messages、:generateContent ...
```

关键点：

- `meta.Mode` 是**请求模式**，不是 Model Type。
- `adaptor.ModeFromMeta` 直接返回 `meta.Mode`（`relay/adaptor/utils.go:61-67`）。
- Model Type 放在 `meta.ModelConfig.Type` 里，只有少数逻辑会主动读取它。
- 请求模式由 URL 决定，不由 Model Type 决定。

## Model Type 的作用

### 1. 端点兼容性校验（第一道门）

`middleware/distributor.go:414-533` 的 `CheckRelayMode` 会按请求模式限制可用的模型类型。LLM 相关大致是：

| 客户端端点 | 允许的 Model Type |
| --- | --- |
| `/v1/chat/completions`、`/v1/messages`、Gemini 原生 | `ChatCompletions` / `Completions` / `Anthropic` / `Gemini` / `GeminiTTS` / `GeminiImage` / `Responses` |
| `/v1/responses` | `ChatCompletions` / `Anthropic` / `Gemini` / `GeminiTTS` / `GeminiImage` / `Responses`（不允许 `Completions`） |
| `/v1/embeddings` | 精确匹配 `Embeddings` |
| `/v1/images/generations` | `ImagesGenerations` / `ImagesEdits` / `GeminiImage` |
| `/v1/images/edits` | `ImagesGenerations` / `ImagesEdits` |
| `/v1/audio/speech` | `AudioSpeech` / `GeminiTTS` |
| `Model Type = Unknown` | 全部允许（不加限制） |

其他模式按 `CheckRelayMode` 的精确/集合匹配执行，例如 `Embeddings`、`Rerank`、`Moderations`、`ParsePdf` 及各种视频模式。

实际执行点在 `controller/relay-controller.go:522-529`：

```go
func checkRelayModeForAttempt(m mode.Mode, modelName string, modelConfig model.ModelConfig) error {
    if middleware.CheckRelayMode(m, modelConfig.Type) {
        return nil
    }
    return relayModeMismatchError{modelName: modelName}
}
```

所以如果模型配成 `Embeddings`，再用 `/v1/chat/completions` 请求，会在渠道选择前就失败；配成 `Completions`，则不能用 `/v1/responses`。

### 2. 触发 Responses-only 自动转换

`IsResponsesOnlyModel`（`relay/adaptor/openai/constants.go:1457-1497`）会检查：

- 模型名是否在 `responsesOnlyModels` 列表里（如 `gpt-5`、`o3`）；
- 或 `ModelConfig.Type` 是否是 `Responses` / `ResponsesGet` / `ResponsesDelete` / `ResponsesCancel` / `ResponsesInputItems`。

命中后，OpenAI 兼容渠道上的 Chat / Claude / Gemini 客户端请求会被自动转成 `/responses`：

- Chat → Responses：`relay/adaptor/openai/chat.go:1329`
- Claude → Responses：`relay/adaptor/openai/claude.go:786`
- Gemini → Responses：`relay/adaptor/openai/gemini.go:816`

这是 Model Type 少数会**实际改变转换行为**的地方。

### 3. Gemini Image / TTS 特殊能力

`relay/adaptor/gemini/openai.go:114-140`：

- `ModelConfig.Type == GeminiImage` → 在 Gemini 渠道上允许图片生成端点，Chat 请求自动补 `responseModalities:["Text","Image"]`。
- `ModelConfig.Type == GeminiTTS` → 在 Gemini 渠道上允许 `/v1/audio/speech`，走 Gemini TTS。
- 即使 Type 不是这两个，模型名里包含 `image` / `tts` 也会被启发式识别；但显式设置 Type 更可靠。

### 4. 渠道测试请求

`relay/utils/testreq.go:24-78` 的 `BuildRequest` 根据 Model Type 决定测试时发什么请求：

- `ChatCompletions` → 发 chat 测试
- `Responses` → 发 responses 测试
- `Anthropic` → 发 messages 测试
- `Gemini` → 发 Gemini 原生测试
- `ImagesGenerations` / `GeminiImage` → 发图片测试
- 未知类型 → 返回 `UnsupportedModelTypeError`

渠道测试流程见 `controller/channel-test.go:965-1026`。如果 Model Type 为空，测试会尝试用内置模型目录 `guessModelConfig` 猜测类型；猜不到就无法构造测试请求。

### 5. 流式超时能力

`model/modelconfig.go:118-125`：

```go
func (c *ModelConfig) SupportStreamTimeout() bool {
    switch c.Type {
    case mode.ChatCompletions, mode.Completions, mode.Anthropic, mode.Responses, mode.Gemini:
        return true
    default:
        return false
    }
}
```

只有这些类型允许 `stream_request_timeout`；图片、音频、视频等类型会在保存时把它清零（`model/modelconfig.go:74-76`）。

### 6. 模型列表 / 排序 / UI 元数据

`SortModelConfigsFunc` 会按 `Owner`、`Type`、`Model` 排序（`model/model_caches.go:331-351`）；Dashboard 也会返回 Type（`controller/dashboard.go:346`）。YAML 里还支持 `type_name` 转 Type（`model/yaml_integration.go:121-201`）。

### 7. `Unknown` 的副作用

`Type = Unknown` 时：

- `CheckRelayMode` 全放行；
- 不会因为 Type 触发 Responses-only 转换（但模型名命中列表仍会触发）；
- 渠道测试无法构造请求；
- 流式超时不被支持；
- Gemini Image/TTS 只靠模型名猜测。

所以 `Unknown` 适合临时/兼容场景，不适合依赖测试和特殊能力的生产模型。

## Channel Provider 的作用

### 1. 选择 adaptor

`relay/adaptors/register.go` 按 `Channel.Type` 注册 adaptor，`adaptors.GetAdaptor(channel.Type)` 取出实现。请求进入 relay 后，`Channel.Type` 是选择请求转换器的第一依据。

### 2. 决定支持哪些请求模式

每个 adaptor 的 `SupportMode` 会检查 `meta.Mode`（请求模式），部分还会检查 `meta.ModelConfig.Type`、模型名或 `meta.ActualModel`：

- OpenAI：几乎全模式（`relay/adaptor/openai/adaptor.go:35-68`）
- Anthropic：Chat / Anthropic / Gemini，无 Responses（`relay/adaptor/anthropic/adaptor.go:37-43`）
- Gemini：Chat / Anthropic / Gemini / Embeddings / GeminiImage / GeminiTTS / GeminiVideo 等（`relay/adaptor/gemini/adaptor.go:33-61`）
- VertexAI：Chat / Anthropic / Gemini / GeminiImage / GeminiTTS / GeminiVideo 等（`relay/adaptor/vertexai/adaptor.go:34-59`）
- Ollama：Chat / Completions / Embeddings（`relay/adaptor/ollama/adaptor.go:31-35`）
- Cohere / Coze：仅 Chat

渠道选择会过滤掉 `SupportMode` 返回 false 的渠道（`controller/relay-channel.go:436-452`）。

### 3. 决定上游 URL、鉴权、头部

Provider 的 adaptor 决定：

- `GetRequestURL`：`/chat/completions`、`/responses`、`/messages`、`:generateContent`、Vertex publisher 路径等；
- `SetupRequestHeader`：`Authorization: Bearer`、`X-Api-Key`、`X-Goog-Api-Key`、Anthropic-Version、beta 等；
- BaseURL、deployment、project/region 等 provider 特有参数。

### 4. 决定请求/响应转换

同一个 Chat 请求：

- OpenAI 渠道 → 透传 `/chat/completions`
- Anthropic 渠道 → 转 Claude `/messages`
- Gemini 渠道 → 转 Gemini `:generateContent`
- Vertex Claude → 转 Claude rawPredict
- Ali / Doubao / Qianfan 等 → 各自兼容端点

具体转换矩阵见 `docs/converts/llm.md`。

### 5. 决定默认模型和 ModelMapping

- `channel.Models` 为空时，用 provider 的默认模型列表（`model/model_caches.go:236-240`）。
- `channel.ModelMapping` 为空时，用 provider 的默认映射（`model/model_caches.go:256-260`）。
- `ActualModel = ModelMapping[OriginModel]`，在 `meta.SetChannel` 中计算（`relay/meta/meta.go:209-232`）。

`ActualModel` 会参与部分能力判断，例如：

- Responses-only 判断会同时检查 `OriginModel` 和 `ActualModel`（`relay/adaptor/openai/constants.go:1483-1497`）；
- Gemini Image/TTS 判断使用 `OriginModel` 优先、`ActualModel` 兜底（`relay/adaptor/gemini/openai.go:96-112`）。

### 6. 决定渠道配置 schema

不同 provider 的渠道配置项不同，例如：

- OpenAI：`responses_first_event_timeout`、`map_reasoning_to_reasoning_content`
- Anthropic：`disable_auto_image_url_to_base64`、`disable_context_management`、`remove_tools_examples`
- Gemini：`safety`、`disable_auto_image_url_to_base64`、`disable_auto_audio_url_to_base64`、`disable_auto_video_url_to_base64`

这些配置由 adaptor 的 `Metadata().ConfigSchema` 提供。

## 两者如何组合

### 例 1：同一个 Claude 模型，两个 provider

内置 Claude 模型通常是 `Type = ChatCompletions`：

- **Claude 模型 + Anthropic 渠道**
  - `/v1/chat/completions` → 转 Claude `/messages`
  - `/v1/messages` → 原生 `/messages`
  - Gemini 原生 → 转 Claude
  - `/v1/responses` → 不支持（Anthropic adaptor 没有 Responses mode）

- **同一个 Claude 模型 + OpenRouter（OpenAI 兼容）渠道**
  - `/v1/chat/completions` → 原生 OpenAI chat
  - `/v1/messages` → 转 OpenAI chat
  - Gemini 原生 → 转 OpenAI chat
  - `/v1/responses` → 透传 OpenRouter 的 `/responses`（上游支持才行）

**Model Type 完全相同，但上游协议完全不同——由 Channel Provider 决定。**

### 例 2：同一个 OpenAI 渠道，不同 Model Type

- `gpt-4o`：`Type = ChatCompletions`（`relay/adaptor/openai/constants.go:1162-1164`）
  - `/v1/chat/completions` → `/chat/completions`
  - `/v1/messages` → 转 chat
  - `/v1/responses` → 透传 `/responses`，**不会降级转 chat**

- `gpt-5`：`Type = Responses`（`relay/adaptor/openai/constants.go:678-680`）
  - `/v1/chat/completions` → 自动转 `/responses`
  - `/v1/messages` → 自动转 `/responses`
  - Gemini 原生 → 自动转 `/responses`
  - `/v1/responses` → 透传

**同一个 Provider，Model Type 改变了转换触发条件。**

### 例 3：Type 写成 Anthropic/Gemini 并不会切换上游

- 模型 `my-claude`，`Type = Anthropic`，但渠道是 OpenAI 兼容渠道：
  - `/v1/messages` 仍然是 **Claude → OpenAI chat**，不会因为 Type=Anthropic 就发 `/messages`。
- 模型 `my-gemini`，`Type = Gemini`，但渠道是 OpenAI 兼容渠道：
  - Gemini 原生请求仍然会被 **转成 OpenAI chat**，不会因为 Type=Gemini 就发 `:generateContent`。

Model Type 只是标签和门禁，**Provider 才是协议选择器**。

### 例 4：ModelMapping 会影响能力判断

`ModelMapping` 会把客户端模型名映射成实际上游模型名：

```yaml
model_mapping:
  my-gpt: gpt-5
```

此时：

- `OriginModel = my-gpt`
- `ActualModel = gpt-5`
- `IsResponsesOnlyModelAny` 会同时检查 `OriginModel` 和 `ActualModel`
- 即使 `my-gpt` 的 `ModelConfig.Type = ChatCompletions`，实际映射到 `gpt-5` 也会触发 Responses 转换

这说明：**Model Type 属于“客户端模型配置”，ActualModel 属于“渠道映射”，两者会共同影响最终行为。**

### 例 5：类型和 provider 不匹配

- `Type = Responses` + Anthropic 渠道：
  - `CheckRelayMode` 对 Responses 请求允许 `Responses` 模型类型；
  - 但 Anthropic adaptor 的 `SupportMode` 不接受 `Responses`；
  - 结果：没有可用渠道，请求失败。

- `Type = Embeddings` + OpenAI 渠道，但请求 `/v1/chat/completions`：
  - `CheckRelayMode` 不允许 `Embeddings` 用于 Chat；
  - 结果：在渠道选择前失败。

- `Type = GeminiImage` + OpenAI 渠道，请求 `/v1/images/generations`：
  - `CheckRelayMode` 允许 `GeminiImage` 用于图片生成；
  - OpenAI adaptor 也支持 `ImagesGenerations`；
  - 结果：会走 **OpenAI 图片协议**，不是 Gemini。Type 不会强制 Gemini 上游。

## 配置建议

1. **普通 Chat LLM**：`Type = ChatCompletions`。
   - Claude、Gemini 的文本模型内置配置也是这个，不需要为了“走 Claude/Gemini 原生”而改成 `Anthropic` / `Gemini`。

2. **Responses-only 模型**：`Type = Responses`。
   - 例如 `gpt-5`、`o3` 这类只能走 `/responses` 的模型。
   - 如果自定义模型名不在内置列表，必须显式配 `Responses`，否则 Chat 请求不会自动转 `/responses`。

3. **Embedding / 图片 / TTS / 视频模型**：配对应类型。
   - `Embeddings`、`ImagesGenerations`、`AudioSpeech`、`GeminiImage`、`GeminiTTS`、`GeminiVideo`、`Videos` 等。
   - 这些类型会严格限制可用端点。

4. **不要用 Model Type 修 Provider 的问题**。
   - Provider 选错，Model Type 改什么都无法把 OpenAI 渠道变成 Anthropic 上游。
   - 例如 `/v1/responses` → Anthropic/Gemini 上游，当前不支持；把 Model Type 改成 `Responses` 也不会让它支持。

5. **Model Type = Unknown 只适合兜底**。
   - 全放行，但失去 Responses-only 类型触发、渠道测试、流超时、Gemini Image/TTS 显式识别。

6. **确认以下四件事**：
   - `channel.Models` 是否包含该模型名；
   - 是否存在对应的 `ModelConfig`（默认情况下，不存在会被 `initializeChannelModels` 从渠道模型列表中过滤掉，`model/model_caches.go:236-254`）；
   - `ModelConfig.Type` 是否兼容当前端点；
   - Provider 的 `SupportMode` 是否支持当前端点。

7. **ModelMapping 的两个注意点**：
   - 常规路由索引由 `channel.Models` 构建（`model/model_caches.go:262-279`），建议把公开模型名同时放进 `Models`，再用 `ModelMapping` 映射到实际模型。
   - `ActualModel` 会参与 Responses-only、Gemini Image/TTS 等能力判断，别名映射到特殊模型时可能改变转换行为。

## 代码索引

| 主题 | 代码位置 |
| --- | --- |
| ModelConfig 定义 | `model/modelconfig.go:29-55` |
| Model Type 枚举 | `relay/mode/define.go` |
| Channel 定义 / Provider 枚举 | `model/channel.go:33-63`、`model/chtype.go` |
| 模式兼容性 | `middleware/distributor.go:414-533` |
| 请求时检查 Model Type | `controller/relay-controller.go:522-529` |
| 渠道 adaptor 过滤 | `controller/relay-channel.go:436-452` |
| adaptor 注册表 | `relay/adaptors/register.go` |
| OpenAI SupportMode | `relay/adaptor/openai/adaptor.go:35-68` |
| Anthropic SupportMode | `relay/adaptor/anthropic/adaptor.go:37-43` |
| Gemini SupportMode | `relay/adaptor/gemini/adaptor.go:33-61` |
| VertexAI SupportMode | `relay/adaptor/vertexai/adaptor.go:34-59` |
| Responses-only 判断 | `relay/adaptor/openai/constants.go:1457-1497` |
| Gemini Image/TTS 判断 | `relay/adaptor/gemini/openai.go:114-140` |
| 渠道测试请求 | `relay/utils/testreq.go:24-78` |
| 渠道测试流程 | `controller/channel-test.go:965-1026` |
| 流式超时 | `model/modelconfig.go:118-125` |
| 渠道模型索引 / 默认模型 | `model/model_caches.go:236-279` |
| 渠道可访问模型 | `model/channel.go:69-86` |
| ActualModel 映射 | `relay/meta/meta.go:209-232` |
| YAML type_name | `model/yaml_integration.go:121-201` |
