package cachefollow

import (
	"net/http"
	"strings"
	"time"

	"github.com/bytedance/sonic/ast"
	"github.com/gin-gonic/gin"
	"github.com/labring/aiproxy/core/common"
	"github.com/labring/aiproxy/core/model"
	"github.com/labring/aiproxy/core/relay/adaptor"
	"github.com/labring/aiproxy/core/relay/meta"
	"github.com/labring/aiproxy/core/relay/mode"
	"github.com/labring/aiproxy/core/relay/plugin"
	"github.com/labring/aiproxy/core/relay/plugin/noop"
	"github.com/labring/aiproxy/core/relay/render"
	"github.com/labring/aiproxy/core/relay/utils"
)

const (
	maxFollowedChannelTTL              = 5 * time.Minute
	defaultFollowedChannelTTL          = 3 * time.Minute
	defaultRecentChannelUpdateDebounce = 45 * time.Second
)

var _ plugin.Plugin = (*Plugin)(nil)

type Plugin struct {
	noop.Noop
	configCache utils.PluginConfigCache[Config]
}

func NewCacheFollowPlugin() plugin.Plugin {
	return &Plugin{}
}

func (p *Plugin) getConfig(meta *meta.Meta) (*Config, error) {
	pluginConfig, err := p.configCache.Load(meta, PluginName, Config{})
	if err != nil {
		return nil, err
	}

	return &pluginConfig, nil
}

func getFollowedChannelTTL(retention string, defaultTTL time.Duration) time.Duration {
	defaultTTL = min(defaultTTL, maxFollowedChannelTTL)

	retention = strings.TrimSpace(strings.ToLower(retention))
	if retention == "" || retention == "in-memory" || retention == "in_memory" {
		return defaultTTL
	}

	ttl, err := time.ParseDuration(retention)
	if err != nil || ttl <= 0 {
		return defaultTTL
	}

	return min(ttl, maxFollowedChannelTTL)
}

func getNodeStringField(node *ast.Node, key string) (string, bool) {
	field := node.Get(key)
	if field == nil || !field.Exists() {
		return "", false
	}

	if field.TypeSafe() == ast.V_NULL {
		return "", true
	}

	value, err := field.String()
	if err != nil {
		return "", true
	}

	return value, true
}

type retentionResponseWriter struct {
	gin.ResponseWriter
	parsed    bool
	retention string
}

func (rw *retentionResponseWriter) Write(b []byte) (int, error) {
	if !rw.parsed {
		rw.tryParseRetention(b)
	}

	return rw.ResponseWriter.Write(b)
}

func (rw *retentionResponseWriter) WriteString(s string) (int, error) {
	return rw.Write([]byte(s))
}

func (rw *retentionResponseWriter) tryParseRetention(data []byte) {
	if render.IsValidSSEData(data) {
		data = render.ExtractSSEData(data)
		if len(data) == 0 || render.IsSSEDone(data) {
			return
		}
	}

	node, err := common.GetJSONNodeNoCopy(data)
	if err != nil || !node.Valid() {
		return
	}

	retention, ok := getNodeStringField(&node, "prompt_cache_retention")
	if ok {
		rw.retention = retention
		rw.parsed = true
		return
	}

	if !ok {
		responseNode := node.Get("response")
		if responseNode != nil && responseNode.Exists() && responseNode.TypeSafe() != ast.V_NULL {
			rw.retention, rw.parsed = getNodeStringField(responseNode, "prompt_cache_retention")
		}
	}
}

func shouldRecord(
	meta *meta.Meta,
	c *gin.Context,
	result adaptor.DoResponseResult,
	relayErr adaptor.Error,
) bool {
	if relayErr != nil {
		return false
	}

	// 不按 cached 命中情况判断：当存在多个后端时，若依赖 cached 判断，
	// 那么多个后端会依次轮询一遍后才会触发到缓存，cachefollow 建立会严重滞后。
	// 因此当前用户触发一次就添加到 cachefollow 中，等待一个 followed ttl 收敛。
	//
	// 原 cached usage 判断（注释保留）：
	// ollama cloud 模型（带有 :cloud / -cloud 后缀）不返回缓存命中 token，跳过缓存 usage 判断。
	// 注意：需同时检查 mapping 后的 ActualModel，否则通过 ModelMapping
	// 把普通模型映射成 :cloud 变体时会漏判（真正决定走云端的是上游模型名）。
	// isCloudModel := isCloudModelName(meta.OriginModel) ||
	// 	isCloudModelName(meta.ActualModel)
	// if !isCloudModel &&
	// 	(result.Usage.CachedTokens <= 0 && result.Usage.CacheCreationTokens <= 0) {
	// 	return false
	// }
	if meta.Channel.ID == 0 || meta.OriginModel == "" {
		return false
	}

	status := c.Writer.Status()
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return false
	}

	if !c.Writer.Written() || c.Writer.Size() <= 0 {
		return false
	}

	return true
}

// isCloudModelName 判断模型名是否为 ollama cloud 变体（:cloud 或 -cloud 后缀）。
// 当前 shouldRecord 中的 cached 判断已注释保留，此函数暂未被调用。
func isCloudModelName(model string) bool {
	return strings.HasSuffix(model, ":cloud") || strings.HasSuffix(model, "-cloud")
}

func saveStableStoreMapping(
	store adaptor.Store,
	id string,
	meta *meta.Meta,
	expiresAt time.Time,
) error {
	if id == "" || meta.Channel.BackupOnly {
		return nil
	}

	now := time.Now()

	return store.SaveIfNotExistStore(adaptor.StoreCache{
		ID:        id,
		GroupID:   meta.Group.ID,
		TokenID:   meta.Token.ID,
		ChannelID: meta.Channel.ID,
		Model:     meta.OriginModel,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: expiresAt,
	}, meta.Channel.Scope)
}

func saveRecentStoreMapping(
	store adaptor.Store,
	id string,
	meta *meta.Meta,
	expiresAt time.Time,
	minInterval time.Duration,
) error {
	if id == "" {
		return nil
	}

	now := time.Now()

	return store.SaveStoreWithOption(
		adaptor.StoreCache{
			ID:        id,
			GroupID:   meta.Group.ID,
			TokenID:   meta.Token.ID,
			ChannelID: meta.Channel.ID,
			Model:     meta.OriginModel,
			CreatedAt: now,
			UpdatedAt: now,
			ExpiresAt: expiresAt,
		},
		meta.Channel.Scope,
		adaptor.SaveStoreOption{
			MinUpdateInterval: minInterval,
		},
	)
}

func savePromptCacheMappings(
	store adaptor.Store,
	meta *meta.Meta,
	expiresAt time.Time,
	minInterval time.Duration,
) error {
	if err := saveStableStoreMapping(
		store,
		model.PromptCacheStoreID(
			meta.OriginModel,
			meta.PromptCacheKey,
			model.CacheKeyTypeStable,
		),
		meta,
		expiresAt,
	); err != nil {
		return err
	}

	return saveRecentStoreMapping(
		store,
		model.PromptCacheStoreID(
			meta.OriginModel,
			meta.PromptCacheKey,
			model.CacheKeyTypeRecent,
		),
		meta,
		expiresAt,
		minInterval,
	)
}

func saveCacheFollowMappings(
	store adaptor.Store,
	meta *meta.Meta,
	expiresAt time.Time,
	minInterval time.Duration,
	enableGenericFollow bool,
) error {
	if meta.User != "" {
		if err := saveStableStoreMapping(
			store,
			model.CacheFollowUserStoreID(meta.OriginModel, meta.User, model.CacheKeyTypeStable),
			meta,
			expiresAt,
		); err != nil {
			return err
		}

		if err := saveRecentStoreMapping(
			store,
			model.CacheFollowUserStoreID(meta.OriginModel, meta.User, model.CacheKeyTypeRecent),
			meta,
			expiresAt,
			minInterval,
		); err != nil {
			return err
		}
	}

	if !enableGenericFollow {
		return nil
	}

	if err := saveStableStoreMapping(
		store,
		model.CacheFollowStoreID(meta.OriginModel, model.CacheKeyTypeStable),
		meta,
		expiresAt,
	); err != nil {
		return err
	}

	return saveRecentStoreMapping(
		store,
		model.CacheFollowStoreID(meta.OriginModel, model.CacheKeyTypeRecent),
		meta,
		expiresAt,
		minInterval,
	)
}

func supportsPromptCacheStore(m mode.Mode) bool {
	switch m {
	case mode.Responses, mode.ResponsesCompact, mode.ChatCompletions:
		return true
	default:
		return false
	}
}

func supportsCacheFollowStore(m mode.Mode) bool {
	switch m {
	case mode.Responses,
		mode.ResponsesCompact,
		mode.ChatCompletions,
		mode.Gemini,
		mode.Anthropic:
		return true
	default:
		return false
	}
}

func (p *Plugin) DoResponse(
	meta *meta.Meta,
	store adaptor.Store,
	c *gin.Context,
	resp *http.Response,
	do adaptor.DoResponse,
) (adaptor.DoResponseResult, adaptor.Error) {
	if !supportsCacheFollowStore(meta.Mode) {
		return do.DoResponse(meta, store, c, resp)
	}

	pluginConfig, err := p.getConfig(meta)
	if err != nil || !pluginConfig.Enable {
		return do.DoResponse(meta, store, c, resp)
	}

	supportsPromptCache := supportsPromptCacheStore(meta.Mode)

	var retentionWriter *retentionResponseWriter
	if meta.PromptCacheKey != "" && supportsPromptCache {
		retentionWriter = &retentionResponseWriter{ResponseWriter: c.Writer}

		c.Writer = retentionWriter
		defer func() {
			c.Writer = retentionWriter.ResponseWriter
		}()
	}

	result, relayErr := do.DoResponse(meta, store, c, resp)
	if !shouldRecord(meta, c, result, relayErr) {
		return result, relayErr
	}

	if meta.PromptCacheKey != "" && supportsPromptCache {
		retention := ""
		if retentionWriter != nil {
			retention = retentionWriter.retention
		}

		expiresAt := time.Now().
			Add(getFollowedChannelTTL(retention, pluginConfig.GetFollowedChannelTTL()))
		if err := savePromptCacheMappings(
			store,
			meta,
			expiresAt,
			pluginConfig.GetRecentChannelUpdateDebounce(),
		); err != nil {
			common.GetLogger(c).Warnf("save prompt cache key store failed: %v", err)
		}
	}

	expiresAt := time.Now().Add(pluginConfig.GetFollowedChannelTTL())
	if err := saveCacheFollowMappings(
		store,
		meta,
		expiresAt,
		pluginConfig.GetRecentChannelUpdateDebounce(),
		pluginConfig.EnableGenericFollow,
	); err != nil {
		common.GetLogger(c).Warnf("save cachefollow store failed: %v", err)
	}

	return result, relayErr
}
