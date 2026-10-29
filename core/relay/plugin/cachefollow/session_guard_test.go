//nolint:testpackage
package cachefollow

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labring/aiproxy/core/model"
	"github.com/labring/aiproxy/core/relay/meta"
	"github.com/labring/aiproxy/core/relay/mode"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionGuardEnabled(t *testing.T) {
	t.Parallel()

	assert.True(t, sessionGuardEnabled(Config{}))
	assert.False(t, sessionGuardEnabled(Config{EnableGenericFollow: true}))
}

func TestSessionChatGuardEnabled(t *testing.T) {
	t.Parallel()

	assert.False(t, sessionChatGuardEnabled(nil))

	for _, tt := range []struct {
		name    string
		configs model.ChannelConfigs
		want    bool
	}{
		{
			name: "config absent",
			want: false,
		},
		{
			name:    "bool true",
			configs: model.ChannelConfigs{sessionChatGuardConfigKey: true},
			want:    true,
		},
		{
			name:    "bool false",
			configs: model.ChannelConfigs{sessionChatGuardConfigKey: false},
			want:    false,
		},
		{
			name:    "string true",
			configs: model.ChannelConfigs{sessionChatGuardConfigKey: "true"},
			want:    false,
		},
		{
			name:    "string 1",
			configs: model.ChannelConfigs{sessionChatGuardConfigKey: "1"},
			want:    false,
		},
		{
			name:    "number 1",
			configs: model.ChannelConfigs{sessionChatGuardConfigKey: 1},
			want:    false,
		},
		{
			name:    "nil value",
			configs: model.ChannelConfigs{sessionChatGuardConfigKey: nil},
			want:    false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &meta.Meta{ChannelConfigs: tt.configs}
			assert.Equal(t, tt.want, sessionChatGuardEnabled(m))
		})
	}
}

func TestCheckSessionGuard(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name       string
		enabled    bool
		chatGuard  bool
		config     Config
		mode       mode.Mode
		body       string
		headers    map[string]string
		wantErrMsg string
	}{
		{
			name:   "responses guard disabled by generic follow",
			config: Config{EnableGenericFollow: true},
			mode:   mode.Responses,
			body:   `{}`,
		},
		{
			name:       "responses guard forced by generic follow disabled",
			mode:       mode.Responses,
			body:       `{}`,
			wantErrMsg: "缺失 prompt_cache_key",
		},
		{
			name:       "responses missing prompt_cache_key",
			enabled:    true,
			mode:       mode.Responses,
			body:       `{}`,
			wantErrMsg: "缺失 prompt_cache_key",
		},
		{
			name:       "responses missing prompt_cache_key field",
			enabled:    true,
			mode:       mode.Responses,
			body:       `{"model":"gpt-5"}`,
			wantErrMsg: "缺失 prompt_cache_key",
		},
		{
			name:       "responses empty prompt_cache_key",
			mode:       mode.Responses,
			body:       `{"prompt_cache_key":""}`,
			wantErrMsg: "缺失 prompt_cache_key",
		},
		{
			name:       "responses null prompt_cache_key",
			mode:       mode.Responses,
			body:       `{"prompt_cache_key":null}`,
			wantErrMsg: "缺失 prompt_cache_key",
		},
		{
			name: "responses valid prompt_cache_key",
			mode: mode.Responses,
			body: `{"prompt_cache_key":"cache-key"}`,
		},
		{
			name:       "anthropic missing metadata.user_id",
			mode:       mode.Anthropic,
			body:       `{"model":"claude"}`,
			wantErrMsg: "缺失 metadata.user_id",
		},
		{
			name:       "anthropic empty metadata",
			mode:       mode.Anthropic,
			body:       `{"metadata":{}}`,
			wantErrMsg: "缺失 metadata.user_id",
		},
		{
			name:       "anthropic empty metadata.user_id",
			mode:       mode.Anthropic,
			body:       `{"metadata":{"user_id":""}}`,
			wantErrMsg: "缺失 metadata.user_id",
		},
		{
			name:       "anthropic null metadata",
			mode:       mode.Anthropic,
			body:       `{"metadata":null}`,
			wantErrMsg: "缺失 metadata.user_id",
		},
		{
			name:       "anthropic null metadata.user_id",
			mode:       mode.Anthropic,
			body:       `{"metadata":{"user_id":null}}`,
			wantErrMsg: "缺失 metadata.user_id",
		},
		{
			name: "anthropic valid metadata.user_id",
			mode: mode.Anthropic,
			body: `{"metadata":{"user_id":"user-1"}}`,
		},
		{
			name: "chat completions chat guard not configured",
			mode: mode.ChatCompletions,
			body: `{}`,
		},
		{
			name:       "chat completions missing session id",
			chatGuard:  true,
			mode:       mode.ChatCompletions,
			body:       `{}`,
			wantErrMsg: "缺失 session-id",
		},
		{
			name:       "chat completions empty session id",
			chatGuard:  true,
			mode:       mode.ChatCompletions,
			body:       `{}`,
			headers:    map[string]string{"x-session-id": ""},
			wantErrMsg: "缺失 session-id",
		},
		{
			name:      "chat completions valid session id",
			chatGuard: true,
			mode:      mode.ChatCompletions,
			body:      `{}`,
			headers:   map[string]string{"session_id": "session-1"},
		},
		{
			name: "unsupported mode",
			mode: mode.Gemini,
			body: `{}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/v1/responses",
				strings.NewReader(tt.body),
			)
			for name, value := range tt.headers {
				req.Header.Set(name, value)
			}
			m := &meta.Meta{
				Mode:           tt.mode,
				ChannelConfigs: model.ChannelConfigs{},
			}
			if tt.chatGuard {
				m.ChannelConfigs[sessionChatGuardConfigKey] = true
			}

			config := tt.config
			if tt.enabled {
				config.EnableGenericFollow = true
			}

			err := checkSessionGuard(m, req, config)
			if tt.wantErrMsg == "" {
				require.NoError(t, err)
				return
			}

			require.EqualError(t, err, tt.wantErrMsg)
		})
	}
}
