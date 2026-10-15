package cachefollow

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/labring/aiproxy/core/relay/adaptor"
	"github.com/labring/aiproxy/core/relay/meta"
)

// opencodeSessionHeader 是 opencode 上游使用的会话 header。
const opencodeSessionHeader = "x-opencode-session"

// sessionAffinityHeaderConfigKey 是 channel configs 中指定目标会话 header 名的字段，
// 用于 opencode 之外的其它上游。
const sessionAffinityHeaderConfigKey = "session_affinity_header"

// sessionAffinitySourceHeaders 是不同客户端可能用来携带会话 id 的原始 header，
// 命中第一个有值的就停，同名多值全部保留。
// 上游 adaptor 只复制固定白名单 header，这些名字大多不在其中，会被丢掉，因此在这里补发。
var sessionAffinitySourceHeaders = []string{
	opencodeSessionHeader,
	"x-session-id",
	"session_id",
	"session-id",
	"x-conversation-id",
	"conversation_id",
	"thread-id",
	"x-client-request-id",
}

// getSessionAffinityHeader 按 channel configs 决定要写到上游的会话 header 名：
// session_affinity_header 显式指定优先；否则 opencode: true 时用 x-opencode-session。
func getSessionAffinityHeader(meta *meta.Meta) string {
	if header, ok := meta.ChannelConfigs[sessionAffinityHeaderConfigKey].(string); ok && header != "" {
		return header
	}

	if enabled, ok := meta.ChannelConfigs["opencode"].(bool); ok && enabled {
		return opencodeSessionHeader
	}

	return ""
}

// setSessionAffinityHeaders 从客户端原始请求 header 解析会话 id，写到上游请求的 headerKey 上。
func setSessionAffinityHeaders(c *gin.Context, req *http.Request, headerKey string) {
	if c == nil || c.Request == nil || req == nil || headerKey == "" {
		return
	}

	// 配置的 headerKey 优先作为来源，再回退到通用客户端 header 列表。
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
}

// SetupRequestHeader 按 channel configs 补发会话 header。
func (p *Plugin) SetupRequestHeader(
	meta *meta.Meta,
	store adaptor.Store,
	c *gin.Context,
	req *http.Request,
	do adaptor.SetupRequestHeader,
) error {
	if header := getSessionAffinityHeader(meta); header != "" {
		setSessionAffinityHeaders(c, req, header)
	}

	return do.SetupRequestHeader(meta, store, c, req)
}
