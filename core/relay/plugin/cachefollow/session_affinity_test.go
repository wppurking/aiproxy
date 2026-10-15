//nolint:testpackage
package cachefollow

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/labring/aiproxy/core/model"
	"github.com/labring/aiproxy/core/relay/adaptor"
	"github.com/labring/aiproxy/core/relay/meta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type setupRequestHeaderFunc struct {
	fn func(*meta.Meta, adaptor.Store, *gin.Context, *http.Request) error
}

func (f setupRequestHeaderFunc) SetupRequestHeader(
	meta *meta.Meta,
	store adaptor.Store,
	c *gin.Context,
	req *http.Request,
) error {
	return f.fn(meta, store, c, req)
}

func TestSetupRequestHeaderForwardsOpencodeSession(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	for _, tt := range []struct {
		name    string
		configs model.ChannelConfigs
		want    []string
	}{
		{
			name:    "opencode format",
			configs: model.ChannelConfigs{sessionAffinityFormatConfigKey: "opencode"},
			want:    []string{"session-a", "session-b"},
		},
		{name: "format absent", configs: model.ChannelConfigs{}},
		{name: "unknown format", configs: model.ChannelConfigs{sessionAffinityFormatConfigKey: "unknown"}},
		{name: "non string format", configs: model.ChannelConfigs{sessionAffinityFormatConfigKey: 123}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/v1/chat/completions",
				nil,
			)
			c.Request.Header.Add("x-opencode-session", "session-a")
			c.Request.Header.Add("x-opencode-session", "session-b")

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"https://upstream.example.com",
				nil,
			)

			chained := false
			err := (&Plugin{}).SetupRequestHeader(
				&meta.Meta{ChannelConfigs: tt.configs},
				&recordingStore{},
				c,
				req,
				setupRequestHeaderFunc{
					fn: func(_ *meta.Meta, _ adaptor.Store, _ *gin.Context, _ *http.Request) error {
						chained = true
						return nil
					},
				},
			)
			require.NoError(t, err)
			assert.True(t, chained)
			assert.Equal(t, tt.want, req.Header.Values("x-opencode-session"))
		})
	}
}

func TestSetupRequestHeaderResolvesClientSessionHeaders(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	for _, tt := range []struct {
		name           string
		promptCacheKey string
		headers        map[string][]string
		want           []string
	}{
		{
			name:           "prompt cache key wins over client header",
			promptCacheKey: "pc",
			headers:        map[string][]string{"x-opencode-session": {"s1"}},
			want:           []string{"pc"},
		},
		{
			name: "native opencode header wins over session_id",
			headers: map[string][]string{
				"x-opencode-session": {"s1", "s2"},
				"session_id":         {"other"},
			},
			want: []string{"s1", "s2"},
		},
		{
			name:    "session_id fallback with multiple values",
			headers: map[string][]string{"session_id": {"s1", "s2"}},
			want:    []string{"s1", "s2"},
		},
		{
			name:    "conversation_id fallback",
			headers: map[string][]string{"conversation_id": {"c1"}},
			want:    []string{"c1"},
		},
		{
			name:    "no session header",
			headers: nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"/v1/chat/completions",
				nil,
			)

			for name, values := range tt.headers {
				for _, value := range values {
					c.Request.Header.Add(name, value)
				}
			}

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"https://upstream.example.com",
				nil,
			)

			err := (&Plugin{}).SetupRequestHeader(
				&meta.Meta{
					ChannelConfigs: model.ChannelConfigs{sessionAffinityFormatConfigKey: "opencode"},
					PromptCacheKey: tt.promptCacheKey,
				},
				&recordingStore{},
				c,
				req,
				setupRequestHeaderFunc{
					fn: func(_ *meta.Meta, _ adaptor.Store, _ *gin.Context, _ *http.Request) error {
						return nil
					},
				},
			)
			require.NoError(t, err)
			assert.Equal(t, tt.want, req.Header.Values("x-opencode-session"))
		})
	}
}

func TestSetupRequestHeaderFallsBackToSessionAffinityKey(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	newRequest := func(t *testing.T) *http.Request {
		t.Helper()

		req := httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"https://upstream.example.com",
			nil,
		)
		require.Empty(t, req.Header.Values("x-opencode-session"))

		return req
	}

	run := func(t *testing.T, m *meta.Meta, req *http.Request) {
		t.Helper()

		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequestWithContext(
			t.Context(),
			http.MethodPost,
			"/v1/chat/completions",
			nil,
		)

		err := (&Plugin{}).SetupRequestHeader(
			m,
			&recordingStore{},
			c,
			req,
			setupRequestHeaderFunc{
				fn: func(_ *meta.Meta, _ adaptor.Store, _ *gin.Context, _ *http.Request) error {
					return nil
				},
			},
		)
		require.NoError(t, err)
	}

	configs := model.ChannelConfigs{sessionAffinityFormatConfigKey: "opencode"}

	t.Run("derives key from route context", func(t *testing.T) {
		t.Parallel()

		req := newRequest(t)
		run(t, &meta.Meta{
			ChannelConfigs: configs,
			Group:          model.GroupCache{ID: "group-1"},
			Token:          model.TokenCache{ID: 7},
			Channel:        meta.ChannelMeta{ID: 33},
			OriginModel:    "gpt-5",
		}, req)

		assert.Equal(t, []string{
			model.HashedStoreID(sessionAffinityKeyPrefix, "group-1", "7", "33", "gpt-5"),
		}, req.Header.Values("x-opencode-session"))
	})

	t.Run("stable across identical route context", func(t *testing.T) {
		t.Parallel()

		m := func() *meta.Meta {
			return &meta.Meta{
				ChannelConfigs: configs,
				Group:          model.GroupCache{ID: "group-1"},
				Token:          model.TokenCache{ID: 7},
				Channel:        meta.ChannelMeta{ID: 33},
				OriginModel:    "gpt-5",
			}
		}

		first, second := newRequest(t), newRequest(t)
		run(t, m(), first)
		run(t, m(), second)
		assert.Equal(t, first.Header.Values("x-opencode-session"), second.Header.Values("x-opencode-session"))
	})

	t.Run("no key without channel and model", func(t *testing.T) {
		t.Parallel()

		req := newRequest(t)
		run(t, &meta.Meta{ChannelConfigs: configs}, req)
		assert.Empty(t, req.Header.Values("x-opencode-session"))
	})
}
