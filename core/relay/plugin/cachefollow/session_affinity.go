package cachefollow

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/labring/aiproxy/core/model"
	"github.com/labring/aiproxy/core/relay/adaptor"
	"github.com/labring/aiproxy/core/relay/meta"
)

// sessionAffinityKeyPrefix 故意留空:回退会话 id 只提交纯哈希,不带像 session_affinity 这类
// 规范化前缀,避免上游从 header 值就认出是我们的路由指纹.
const sessionAffinityKeyPrefix = ""

// sessionAffinityFormatConfigKey 是 channel configs 中指定上游格式的字段.
// 取值是上游的 format 名,例如 openai,opencode.
const sessionAffinityFormatConfigKey = "session_affinity_format"

// sessionAffinityFormatHeaders 保存不同上游 format 对应的目标会话 header 名.
var sessionAffinityFormatHeaders = map[string]string{
	"opencode": "x-opencode-session",
}

// sessionAffinitySourceHeaders 是不同客户端可能用来携带会话 id 的原始 header,
// 命中第一个有值的就停,同名多值全部保留.
var sessionAffinitySourceHeaders = []string{
	"x-session-id",
	"session_id",
	"session-id",
	"x-session-affinity",
	"x-conversation-id",
	"conversation_id",
}

// getSessionAffinityHeader 按 channel configs 的 session_affinity_format 返回上游会话 header 名.
func getSessionAffinityHeader(meta *meta.Meta) string {
	format, ok := meta.ChannelConfigs[sessionAffinityFormatConfigKey].(string)
	if !ok {
		return ""
	}

	return sessionAffinityFormatHeaders[format]
}

// sessionAffinityKey 在客户端没有携带任何会话 header 时,用本次路由上下文算一个稳定 key,
// 让同一 group/token/channel/model 的路由复用同一个上游会话,从而保住上游 cache.
func sessionAffinityKey(m *meta.Meta) string {
	if m == nil || m.Channel.ID == 0 || m.OriginModel == "" {
		return ""
	}

	return model.HashedStoreID(
		sessionAffinityKeyPrefix,
		m.Group.ID,
		strconv.Itoa(m.Token.ID),
		strconv.Itoa(m.Channel.ID),
		m.OriginModel,
	)
}

// setSessionAffinityHeaders 解析会话 id 并只写到上游请求的 headerKey 上.
// 取值优先级:prompt cache key > 客户端 format key > 通用 session id 系列 > 路由推导 key.
func setSessionAffinityHeaders(m *meta.Meta, c *gin.Context, req *http.Request, headerKey string) {
	if req == nil || headerKey == "" {
		return
	}

	if m != nil && m.PromptCacheKey != "" {
		req.Header.Set(headerKey, m.PromptCacheKey)

		return
	}

	if c == nil || c.Request == nil {
		return
	}

	for _, name := range append([]string{headerKey}, sessionAffinitySourceHeaders...) {
		values := c.Request.Header.Values(name)
		if len(values) == 0 {
			continue
		}

		for _, value := range values {
			req.Header.Add(headerKey, value)
		}

		return
	}

	if key := sessionAffinityKey(m); key != "" {
		req.Header.Set(headerKey, key)
	}
}

// SetupRequestHeader 按 channel configs 补发会话 header.
func (p *Plugin) SetupRequestHeader(
	meta *meta.Meta,
	store adaptor.Store,
	c *gin.Context,
	req *http.Request,
	do adaptor.SetupRequestHeader,
) error {
	if header := getSessionAffinityHeader(meta); header != "" {
		setSessionAffinityHeaders(meta, c, req, header)
	}

	return do.SetupRequestHeader(meta, store, c, req)
}
