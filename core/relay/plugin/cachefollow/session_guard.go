package cachefollow

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labring/aiproxy/core/common"
	"github.com/labring/aiproxy/core/relay/adaptor"
	"github.com/labring/aiproxy/core/relay/meta"
	"github.com/labring/aiproxy/core/relay/mode"
)

const (
	// sessionGuardConfigKey 是 channel configs 中开启 session guard 的字段.
	sessionGuardConfigKey = "session_guard"

	// promptCacheKeyField 是 response api 请求中用于会话保持的字段.
	promptCacheKeyField = "prompt_cache_key"

	// anthropicUserIDField 是 anthropic message api 请求中用于会话保持的字段.
	anthropicUserIDField = "user_id"
)

// sessionGuardEnabled 判断当前 channel 是否开启 session guard.
// EnableGenericFollow 关闭时强制开启 session guard；开启时按 channel config 决定.
func sessionGuardEnabled(m *meta.Meta, config Config) bool {
	if m == nil {
		return false
	}

	if !config.EnableGenericFollow {
		return true
	}

	enabled, _ := m.ChannelConfigs[sessionGuardConfigKey].(bool)

	return enabled
}

// checkSessionGuard 是 session guard 的检查入口.
func checkSessionGuard(m *meta.Meta, req *http.Request, config Config) error {
	if !sessionGuardEnabled(m, config) {
		return nil
	}

	switch m.Mode {
	case mode.Responses:
		return checkResponsesSessionGuard(req)
	case mode.Anthropic:
		return checkAnthropicSessionGuard(req)
		// 暂时只针对 response 和 anthropic 两类消息进行处理
	//case mode.ChatCompletions:
	//	return checkChatCompletionsSessionGuard(req)
	default:
		return nil
	}
}

// checkResponsesSessionGuard 检查 response api 请求是否携带了非空的 prompt_cache_key.
func checkResponsesSessionGuard(req *http.Request) error {
	node, err := common.UnmarshalRequest2NodeReusable(req)
	if err != nil {
		return err
	}

	promptCacheKey, ok := getNodeStringField(&node, promptCacheKeyField)
	if !ok || promptCacheKey == "" {
		return errors.New("缺失 prompt_cache_key")
	}

	return nil
}

// checkAnthropicSessionGuard 检查 anthropic message api 请求是否携带了非空的 metadata.user_id.
func checkAnthropicSessionGuard(req *http.Request) error {
	node, err := common.UnmarshalRequest2NodeReusable(req)
	if err != nil {
		return err
	}

	userID, ok := getNodeStringField(&node, "metadata", anthropicUserIDField)
	if !ok || userID == "" {
		return errors.New("缺失 metadata.user_id")
	}

	return nil
}

// checkChatCompletionsSessionGuard 检查 chat completions 请求是否携带了非空的会话 header.
func checkChatCompletionsSessionGuard(req *http.Request) error {
	if req != nil {
		for _, name := range sessionAffinitySourceHeaders {
			for _, value := range req.Header.Values(name) {
				if strings.TrimSpace(value) != "" {
					return nil
				}
			}
		}
	}

	return errors.New("缺失 session-id")
}

// ConvertRequest 在构造上游请求前执行 session guard 检查.
func (p *Plugin) ConvertRequest(
	meta *meta.Meta,
	store adaptor.Store,
	req *http.Request,
	do adaptor.ConvertRequest,
) (adaptor.ConvertResult, error) {
	pluginConfig, err := p.getConfig(meta)
	if err != nil {
		return do.ConvertRequest(meta, store, req)
	}

	if err := checkSessionGuard(meta, req, *pluginConfig); err != nil {
		return adaptor.ConvertResult{}, err
	}

	return do.ConvertRequest(meta, store, req)
}
