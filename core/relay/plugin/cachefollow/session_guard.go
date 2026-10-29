package cachefollow

import (
	"net/http"

	"github.com/labring/aiproxy/core/relay/adaptor"
	"github.com/labring/aiproxy/core/relay/meta"
)

// sessionGuardConfigKey 是 channel configs 中开启 session guard 的字段.
const sessionGuardConfigKey = "session_guard"

// sessionGuardEnabled 判断当前 channel 是否开启 session guard.
func sessionGuardEnabled(m *meta.Meta) bool {
	if m == nil {
		return false
	}

	enabled, _ := m.ChannelConfigs[sessionGuardConfigKey].(bool)

	return enabled
}

// checkSessionGuard 是 session guard 的检查入口.
// TODO: 后续实现针对 chat.completions、responses、messages 的会话保持信息检查.
func checkSessionGuard(m *meta.Meta, req *http.Request) error {
	if !sessionGuardEnabled(m) {
		return nil
	}

	_ = req

	return nil
}

// ConvertRequest 在构造上游请求前执行 session guard 检查.
func (p *Plugin) ConvertRequest(
	meta *meta.Meta,
	store adaptor.Store,
	req *http.Request,
	do adaptor.ConvertRequest,
) (adaptor.ConvertResult, error) {
	if err := checkSessionGuard(meta, req); err != nil {
		return adaptor.ConvertResult{}, err
	}

	return do.ConvertRequest(meta, store, req)
}
