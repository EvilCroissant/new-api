package service

import (
	"fmt"
	"hash/fnv"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/cachex"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/hot"
	"github.com/tidwall/gjson"
)

const (
	ginKeyChannelAffinityCacheKey   = "channel_affinity_cache_key"
	ginKeyChannelAffinityTTLSeconds = "channel_affinity_ttl_seconds"
	ginKeyChannelAffinityMeta       = "channel_affinity_meta"
	ginKeyChannelAffinityLogInfo    = "channel_affinity_log_info"
	ginKeyChannelAffinitySkipRetry  = "channel_affinity_skip_retry_on_failure"
	ginKeyChannelAffinitySelection  = "channel_affinity_selection"
	ginKeyChannelAffinityState      = "channel_affinity_state"
	ginKeyChannelAffinityProbeID    = "channel_affinity_probe_channel_id"

	channelAffinityUsageCacheStatsNamespace = "new-api:channel_affinity_usage_cache_stats:v1"
)

const (
	channelAffinityFRTSlowCountThreshold = 2
	channelAffinityFRTAcceptableMs       = 10_000.0
	channelAffinityFRTExplosionMs        = 20_000.0
	channelAffinityFRTSlowMultiplier     = 1.5
	channelAffinityFRTMaxThresholdMs     = channelAffinityFRTAcceptableMs * channelAffinityFRTSlowMultiplier
	channelAffinityFRTRecentSampleLimit  = 5
	// Keep unknown channels neutral: slower than a normal channel, but still
	// eligible when the current affinity channel is persistently congested.
	channelAffinityFRTColdStartMs   = 7_500.0
	channelAffinityFRTPeakDecay     = 3 * time.Minute
	channelAffinityFRTStateTTL      = 30 * time.Minute
	channelAffinityFRTProbeCooldown = 5 * time.Minute
)

var (
	channelAffinityUsageCacheStatsOnce  sync.Once
	channelAffinityUsageCacheStatsCache *cachex.HybridCache[ChannelAffinityUsageCacheCounters]

	channelAffinityRegexCache sync.Map // map[string]*regexp.Regexp
)

type channelAffinityMeta struct {
	CacheKey       string
	TTLSeconds     int
	RuleName       string
	SkipRetry      bool
	ParamTemplate  map[string]interface{}
	KeySourceType  string
	KeySourceKey   string
	KeySourcePath  string
	KeyHint        string
	KeyFingerprint string
	UsingGroup     string
	ModelName      string
	RequestPath    string
}

type channelAffinitySelection struct {
	Group    string
	Priority int64
}

type ChannelAffinityStatsContext struct {
	RuleName       string
	UsingGroup     string
	KeyFingerprint string
	TTLSeconds     int64
}

const (
	cacheTokenRateModeCachedOverPrompt           = "cached_over_prompt"
	cacheTokenRateModeCachedOverPromptPlusCached = "cached_over_prompt_plus_cached"
	cacheTokenRateModeMixed                      = "mixed"
)

type ChannelAffinityCacheStats struct {
	Enabled       bool           `json:"enabled"`
	Total         int            `json:"total"`
	Unknown       int            `json:"unknown"`
	ByRuleName    map[string]int `json:"by_rule_name"`
	CacheCapacity int            `json:"cache_capacity"`
	CacheAlgo     string         `json:"cache_algo"`
}

func GetChannelAffinityCacheStats() ChannelAffinityCacheStats {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil {
		return ChannelAffinityCacheStats{
			Enabled:    false,
			Total:      0,
			Unknown:    0,
			ByRuleName: map[string]int{},
		}
	}

	cache := getChannelAffinityCache()
	mainCap, _ := cache.Capacity()
	mainAlgo, _ := cache.Algorithm()

	rules := setting.Rules
	ruleByName := make(map[string]operation_setting.ChannelAffinityRule, len(rules))
	for _, r := range rules {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		if !r.IncludeRuleName {
			continue
		}
		ruleByName[name] = r
	}

	byRuleName := make(map[string]int, len(ruleByName))
	for name := range ruleByName {
		byRuleName[name] = 0
	}

	keys, err := cache.Keys()
	if err != nil {
		common.SysError(fmt.Sprintf("channel affinity cache list keys failed: err=%v", err))
		keys = nil
	}
	total := len(keys)
	unknown := 0
	for _, k := range keys {
		prefix := channelAffinityCacheNamespace + ":"
		if !strings.HasPrefix(k, prefix) {
			unknown++
			continue
		}
		rest := strings.TrimPrefix(k, prefix)
		parts := strings.Split(rest, ":")
		if len(parts) < 2 {
			unknown++
			continue
		}
		ruleName := parts[0]
		rule, ok := ruleByName[ruleName]
		if !ok {
			unknown++
			continue
		}
		if rule.IncludeModelName {
			if len(parts) < 3 {
				unknown++
				continue
			}
		}
		if rule.IncludeUsingGroup {
			minParts := 3
			if rule.IncludeModelName {
				minParts = 4
			}
			if len(parts) < minParts {
				unknown++
				continue
			}
		}
		byRuleName[ruleName]++
	}

	return ChannelAffinityCacheStats{
		Enabled:       setting.Enabled,
		Total:         total,
		Unknown:       unknown,
		ByRuleName:    byRuleName,
		CacheCapacity: mainCap,
		CacheAlgo:     mainAlgo,
	}
}

func ClearChannelAffinityCacheAll() int {
	cache := getChannelAffinityCache()
	keys, err := cache.Keys()
	if err != nil {
		common.SysError(fmt.Sprintf("channel affinity cache list keys failed: err=%v", err))
		keys = nil
	}
	if len(keys) > 0 {
		if _, err := cache.DeleteMany(keys); err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache delete many failed: err=%v", err))
		}
	}
	return len(keys)
}

func ClearChannelAffinityCacheByRuleName(ruleName string) (int, error) {
	ruleName = strings.TrimSpace(ruleName)
	if ruleName == "" {
		return 0, fmt.Errorf("rule_name 不能为空")
	}

	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil {
		return 0, fmt.Errorf("channel_affinity_setting 未初始化")
	}

	var matchedRule *operation_setting.ChannelAffinityRule
	for i := range setting.Rules {
		r := &setting.Rules[i]
		if strings.TrimSpace(r.Name) != ruleName {
			continue
		}
		matchedRule = r
		break
	}
	if matchedRule == nil {
		return 0, fmt.Errorf("未知规则名称")
	}
	if !matchedRule.IncludeRuleName {
		return 0, fmt.Errorf("该规则未启用 include_rule_name，无法按规则清空缓存")
	}

	cache := getChannelAffinityCache()
	deleted, err := cache.DeleteByPrefix(ruleName)
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func matchAnyRegexCached(patterns []string, s string) bool {
	if len(patterns) == 0 || s == "" {
		return false
	}
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		re, ok := channelAffinityRegexCache.Load(pattern)
		if !ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				continue
			}
			re = compiled
			channelAffinityRegexCache.Store(pattern, re)
		}
		if re.(*regexp.Regexp).MatchString(s) {
			return true
		}
	}
	return false
}

func matchAnyIncludeFold(patterns []string, s string) bool {
	if len(patterns) == 0 || s == "" {
		return false
	}
	sLower := strings.ToLower(s)
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(sLower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func extractChannelAffinityValue(c *gin.Context, src operation_setting.ChannelAffinityKeySource) string {
	switch src.Type {
	case "context_int":
		if src.Key == "" {
			return ""
		}
		v := c.GetInt(src.Key)
		if v <= 0 {
			return ""
		}
		return strconv.Itoa(v)
	case "context_string":
		if src.Key == "" {
			return ""
		}
		return strings.TrimSpace(c.GetString(src.Key))
	case "request_header":
		if c == nil || c.Request == nil || src.Key == "" {
			return ""
		}
		return strings.TrimSpace(c.Request.Header.Get(src.Key))
	case "gjson":
		if src.Path == "" {
			return ""
		}
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return ""
		}
		body, err := storage.Bytes()
		if err != nil || len(body) == 0 {
			return ""
		}
		res := gjson.GetBytes(body, src.Path)
		if !res.Exists() {
			return ""
		}
		switch res.Type {
		case gjson.String, gjson.Number, gjson.True, gjson.False:
			return strings.TrimSpace(res.String())
		default:
			return strings.TrimSpace(res.Raw)
		}
	default:
		return ""
	}
}

func buildChannelAffinityCacheKeySuffix(rule operation_setting.ChannelAffinityRule, modelName string, usingGroup string, affinityValue string) string {
	parts := make([]string, 0, 4)
	if rule.IncludeRuleName && rule.Name != "" {
		parts = append(parts, rule.Name)
	}
	if rule.IncludeModelName && modelName != "" {
		parts = append(parts, modelName)
	}
	if rule.IncludeUsingGroup && usingGroup != "" {
		parts = append(parts, usingGroup)
	}
	parts = append(parts, affinityValue)
	return strings.Join(parts, ":")
}

func setChannelAffinityContext(c *gin.Context, meta channelAffinityMeta) {
	c.Set(ginKeyChannelAffinityCacheKey, meta.CacheKey)
	c.Set(ginKeyChannelAffinityTTLSeconds, meta.TTLSeconds)
	c.Set(ginKeyChannelAffinityMeta, meta)
}

func getChannelAffinityContext(c *gin.Context) (string, int, bool) {
	keyAny, ok := c.Get(ginKeyChannelAffinityCacheKey)
	if !ok {
		return "", 0, false
	}
	key, ok := keyAny.(string)
	if !ok || key == "" {
		return "", 0, false
	}
	ttlAny, ok := c.Get(ginKeyChannelAffinityTTLSeconds)
	if !ok {
		return key, 0, true
	}
	ttlSeconds, _ := ttlAny.(int)
	return key, ttlSeconds, true
}

func getChannelAffinityMeta(c *gin.Context) (channelAffinityMeta, bool) {
	anyMeta, ok := c.Get(ginKeyChannelAffinityMeta)
	if !ok {
		return channelAffinityMeta{}, false
	}
	meta, ok := anyMeta.(channelAffinityMeta)
	if !ok {
		return channelAffinityMeta{}, false
	}
	return meta, true
}

func GetChannelAffinityStatsContext(c *gin.Context) (ChannelAffinityStatsContext, bool) {
	if c == nil {
		return ChannelAffinityStatsContext{}, false
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return ChannelAffinityStatsContext{}, false
	}
	ruleName := strings.TrimSpace(meta.RuleName)
	keyFp := strings.TrimSpace(meta.KeyFingerprint)
	usingGroup := strings.TrimSpace(meta.UsingGroup)
	if ruleName == "" || keyFp == "" {
		return ChannelAffinityStatsContext{}, false
	}
	ttlSeconds := int64(meta.TTLSeconds)
	if ttlSeconds <= 0 {
		return ChannelAffinityStatsContext{}, false
	}
	return ChannelAffinityStatsContext{
		RuleName:       ruleName,
		UsingGroup:     usingGroup,
		KeyFingerprint: keyFp,
		TTLSeconds:     ttlSeconds,
	}, true
}

func affinityFingerprint(s string) string {
	if s == "" {
		return ""
	}
	hex := common.Sha1([]byte(s))
	if len(hex) >= 8 {
		return hex[:8]
	}
	return hex
}

func buildChannelAffinityKeyHint(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) <= 12 {
		return s
	}
	return s[:4] + "..." + s[len(s)-4:]
}

func cloneStringAnyMap(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeChannelOverride(base map[string]interface{}, tpl map[string]interface{}) map[string]interface{} {
	if len(base) == 0 && len(tpl) == 0 {
		return map[string]interface{}{}
	}
	if len(tpl) == 0 {
		return base
	}
	out := cloneStringAnyMap(base)
	for k, v := range tpl {
		if strings.EqualFold(strings.TrimSpace(k), "operations") {
			baseOps, hasBaseOps := extractParamOperations(out[k])
			tplOps, hasTplOps := extractParamOperations(v)
			if hasTplOps {
				if hasBaseOps {
					out[k] = append(tplOps, baseOps...)
				} else {
					out[k] = tplOps
				}
				continue
			}
		}
		if _, exists := out[k]; exists {
			continue
		}
		out[k] = v
	}
	return out
}

func extractParamOperations(value interface{}) ([]interface{}, bool) {
	switch ops := value.(type) {
	case []interface{}:
		if len(ops) == 0 {
			return []interface{}{}, true
		}
		cloned := make([]interface{}, 0, len(ops))
		cloned = append(cloned, ops...)
		return cloned, true
	case []map[string]interface{}:
		cloned := make([]interface{}, 0, len(ops))
		for _, op := range ops {
			cloned = append(cloned, op)
		}
		return cloned, true
	default:
		return nil, false
	}
}

func appendChannelAffinityTemplateAdminInfo(c *gin.Context, meta channelAffinityMeta) {
	if c == nil {
		return
	}
	if len(meta.ParamTemplate) == 0 {
		return
	}

	templateInfo := map[string]interface{}{
		"applied":             true,
		"rule_name":           meta.RuleName,
		"param_override_keys": len(meta.ParamTemplate),
	}
	if anyInfo, ok := c.Get(ginKeyChannelAffinityLogInfo); ok {
		if info, ok := anyInfo.(map[string]interface{}); ok {
			info["override_template"] = templateInfo
			c.Set(ginKeyChannelAffinityLogInfo, info)
			return
		}
	}
	c.Set(ginKeyChannelAffinityLogInfo, map[string]interface{}{
		"reason":            meta.RuleName,
		"rule_name":         meta.RuleName,
		"using_group":       meta.UsingGroup,
		"model":             meta.ModelName,
		"request_path":      meta.RequestPath,
		"key_source":        meta.KeySourceType,
		"key_key":           meta.KeySourceKey,
		"key_path":          meta.KeySourcePath,
		"key_hint":          meta.KeyHint,
		"key_fp":            meta.KeyFingerprint,
		"override_template": templateInfo,
	})
}

// ApplyChannelAffinityOverrideTemplate merges per-rule channel override templates onto the selected channel override config.
func ApplyChannelAffinityOverrideTemplate(c *gin.Context, paramOverride map[string]interface{}) (map[string]interface{}, bool) {
	if c == nil {
		return paramOverride, false
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return paramOverride, false
	}
	if len(meta.ParamTemplate) == 0 {
		return paramOverride, false
	}

	mergedParam := mergeChannelOverride(paramOverride, meta.ParamTemplate)
	appendChannelAffinityTemplateAdminInfo(c, meta)
	return mergedParam, true
}

func GetPreferredChannelByAffinity(c *gin.Context, modelName string, usingGroup string) (int, bool) {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil || !setting.Enabled {
		return 0, false
	}
	path := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	userAgent := ""
	if c != nil && c.Request != nil {
		userAgent = c.Request.UserAgent()
	}

	for _, rule := range setting.Rules {
		if !matchAnyRegexCached(rule.ModelRegex, modelName) {
			continue
		}
		if len(rule.PathRegex) > 0 && !matchAnyRegexCached(rule.PathRegex, path) {
			continue
		}
		if len(rule.UserAgentInclude) > 0 && !matchAnyIncludeFold(rule.UserAgentInclude, userAgent) {
			continue
		}
		var affinityValue string
		var usedSource operation_setting.ChannelAffinityKeySource
		for _, src := range rule.KeySources {
			affinityValue = extractChannelAffinityValue(c, src)
			if affinityValue != "" {
				usedSource = src
				break
			}
		}
		if affinityValue == "" {
			continue
		}
		if rule.ValueRegex != "" && !matchAnyRegexCached([]string{rule.ValueRegex}, affinityValue) {
			continue
		}

		ttlSeconds := rule.TTLSeconds
		if ttlSeconds <= 0 {
			ttlSeconds = setting.DefaultTTLSeconds
		}
		cacheKeySuffix := buildChannelAffinityCacheKeySuffix(rule, modelName, usingGroup, affinityValue)
		cacheKeyFull := channelAffinityCacheNamespace + ":" + cacheKeySuffix
		setChannelAffinityContext(c, channelAffinityMeta{
			CacheKey:       cacheKeyFull,
			TTLSeconds:     ttlSeconds,
			RuleName:       rule.Name,
			SkipRetry:      rule.SkipRetryOnFailure,
			ParamTemplate:  cloneStringAnyMap(rule.ParamOverrideTemplate),
			KeySourceType:  strings.TrimSpace(usedSource.Type),
			KeySourceKey:   strings.TrimSpace(usedSource.Key),
			KeySourcePath:  strings.TrimSpace(usedSource.Path),
			KeyHint:        buildChannelAffinityKeyHint(affinityValue),
			KeyFingerprint: affinityFingerprint(affinityValue),
			UsingGroup:     usingGroup,
			ModelName:      modelName,
			RequestPath:    path,
		})

		state, found, err := getChannelAffinityCache().Get(cacheKeySuffix)
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache get failed: key=%s, err=%v", cacheKeyFull, err))
			return 0, false
		}
		c.Set(ginKeyChannelAffinityState, channelAffinityRequestState{
			State: state,
			Found: found,
		})
		if found {
			return state.ChannelID, true
		}
		return 0, false
	}
	return 0, false
}

func ShouldSkipRetryAfterChannelAffinityFailure(c *gin.Context) bool {
	if c == nil {
		return false
	}
	v, ok := c.Get(ginKeyChannelAffinitySkipRetry)
	if ok {
		b, ok := v.(bool)
		if ok {
			return b
		}
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return false
	}
	return meta.SkipRetry
}

func ClearCurrentChannelAffinityCache(c *gin.Context) bool {
	if c == nil {
		return false
	}
	cacheKey, _, ok := getChannelAffinityContext(c)
	if !ok || cacheKey == "" {
		return false
	}

	requestState, hasState := getChannelAffinityRequestState(c)
	deleted := false
	var err error
	if hasState && requestState.Found {
		deleted, err = deleteChannelAffinityState(cacheKey, requestState.State)
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache delete current failed: err=%v", err))
		}
	}
	c.Set(ginKeyChannelAffinityState, channelAffinityRequestState{})
	c.Set(ginKeyChannelAffinitySelection, nil)
	c.Set(ginKeyChannelAffinityProbeID, 0)
	c.Set(ginKeyChannelAffinitySkipRetry, false)
	return deleted
}

func ShouldKeepChannelAffinityOnChannelDisabled() bool {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil {
		return false
	}
	return setting.KeepOnChannelDisabled
}

func MarkChannelAffinityUsed(c *gin.Context, selectedGroup string, channelID int, priority int64) {
	if c == nil || channelID <= 0 {
		return
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok {
		return
	}
	c.Set(ginKeyChannelAffinitySelection, channelAffinitySelection{
		Group:    selectedGroup,
		Priority: priority,
	})
	c.Set(ginKeyChannelAffinitySkipRetry, meta.SkipRetry)
	info := map[string]interface{}{
		"reason":         meta.RuleName,
		"rule_name":      meta.RuleName,
		"using_group":    meta.UsingGroup,
		"selected_group": selectedGroup,
		"model":          meta.ModelName,
		"request_path":   meta.RequestPath,
		"channel_id":     channelID,
		"priority":       priority,
		"key_source":     meta.KeySourceType,
		"key_key":        meta.KeySourceKey,
		"key_path":       meta.KeySourcePath,
		"key_hint":       meta.KeyHint,
		"key_fp":         meta.KeyFingerprint,
	}
	c.Set(ginKeyChannelAffinityLogInfo, info)
}

func getChannelAffinitySelection(c *gin.Context) (channelAffinitySelection, bool) {
	if c == nil {
		return channelAffinitySelection{}, false
	}
	value, ok := c.Get(ginKeyChannelAffinitySelection)
	if !ok {
		return channelAffinitySelection{}, false
	}
	selection, ok := value.(channelAffinitySelection)
	if !ok {
		return channelAffinitySelection{}, false
	}
	return selection, true
}

func getChannelAffinityRequestState(c *gin.Context) (channelAffinityRequestState, bool) {
	if c == nil {
		return channelAffinityRequestState{}, false
	}
	value, ok := c.Get(ginKeyChannelAffinityState)
	if !ok {
		return channelAffinityRequestState{}, false
	}
	requestState, ok := value.(channelAffinityRequestState)
	return requestState, ok
}

func TryClaimHigherPriorityAffinityProbe(c *gin.Context, selectedGroup string, preferred *model.Channel) *model.Channel {
	if c == nil || preferred == nil || selectedGroup == "" {
		return nil
	}
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil || !setting.Enabled || !setting.OptimizationEnabled {
		return nil
	}
	meta, hasMeta := getChannelAffinityMeta(c)
	requestState, hasState := getChannelAffinityRequestState(c)
	if !hasMeta || !hasState || !requestState.Found ||
		requestState.State.ChannelID != preferred.Id {
		return nil
	}

	now := time.Now()
	nowMillis := now.UnixMilli()
	if requestState.State.NextProbeAt > nowMillis {
		return nil
	}

	nextProbeAt := now.Add(channelAffinityProbeInterval(setting)).UnixMilli()
	claimed, err := claimChannelAffinityProbe(meta.CacheKey, requestState.State, nowMillis, nextProbeAt)
	if err != nil {
		common.SysError(fmt.Sprintf("channel affinity probe claim failed: key=%s, err=%v", meta.CacheKey, err))
		return nil
	}
	if !claimed {
		return nil
	}

	next, err := model.GetRandomSatisfiedChannelAtNextHigherPriority(
		selectedGroup,
		meta.ModelName,
		preferred.GetPriority(),
		meta.RequestPath,
	)
	if err != nil {
		common.SysError(fmt.Sprintf(
			"channel affinity upward probe lookup failed: group=%s, model=%s, err=%v",
			selectedGroup,
			meta.ModelName,
			err,
		))
		return nil
	}
	if next != nil {
		c.Set(ginKeyChannelAffinityProbeID, next.Id)
	}
	return next
}

func AppendChannelAffinityAdminInfo(c *gin.Context, adminInfo map[string]interface{}, requestSucceeded bool) {
	if c == nil || adminInfo == nil {
		return
	}
	anyInfo, ok := c.Get(ginKeyChannelAffinityLogInfo)
	if !ok || anyInfo == nil {
		return
	}
	info, ok := anyInfo.(map[string]interface{})
	if !ok {
		adminInfo["channel_affinity"] = anyInfo
		return
	}
	probeChannelID := c.GetInt(ginKeyChannelAffinityProbeID)
	if probeChannelID > 0 {
		requestState, hasState := getChannelAffinityRequestState(c)
		if hasState && requestState.Found {
			info["event"] = "upward_probe"
			info["from_channel_id"] = requestState.State.ChannelID
			info["to_channel_id"] = probeChannelID
			info["probe_succeeded"] = requestSucceeded && c.GetInt("channel_id") == probeChannelID
		}
	}
	adminInfo["channel_affinity"] = info
}

func RecordChannelAffinity(c *gin.Context, channelID int) {
	if channelID <= 0 {
		return
	}
	setting := operation_setting.GetChannelAffinitySetting()
	if setting == nil || !setting.Enabled {
		return
	}
	if setting.SwitchOnSuccess && c != nil {
		if successChannelID := c.GetInt("channel_id"); successChannelID > 0 {
			channelID = successChannelID
		}
	} else if c != nil {
		probeChannelID := c.GetInt(ginKeyChannelAffinityProbeID)
		successChannelID := c.GetInt("channel_id")
		if probeChannelID > 0 && successChannelID > 0 && successChannelID != probeChannelID {
			requestState, ok := getChannelAffinityRequestState(c)
			if ok && requestState.Found {
				channelID = requestState.State.ChannelID
			}
		}
	}
	cacheKey, ttlSeconds, ok := getChannelAffinityContext(c)
	if !ok {
		return
	}
	if ttlSeconds <= 0 {
		ttlSeconds = setting.DefaultTTLSeconds
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	requestState, hasState := getChannelAffinityRequestState(c)
	if !hasState {
		return
	}

	now := time.Now()
	ttl := time.Duration(ttlSeconds) * time.Second
	idleExpiresAt := now.Add(ttl).UnixMilli()
	if !requestState.Found {
		versionEpoch, versionSeq := nextChannelAffinityVersion()
		state := channelAffinityState{
			ChannelID:     channelID,
			VersionEpoch:  versionEpoch,
			VersionSeq:    versionSeq,
			NextProbeAt:   now.Add(channelAffinityProbeInterval(setting)).UnixMilli(),
			IdleExpiresAt: idleExpiresAt,
		}
		if _, err := createChannelAffinityState(cacheKey, state, ttl); err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache create failed: key=%s, err=%v", cacheKey, err))
		}
		return
	}

	if channelID == requestState.State.ChannelID {
		if _, err := refreshChannelAffinityState(cacheKey, requestState.State, idleExpiresAt, ttl); err != nil {
			common.SysError(fmt.Sprintf("channel affinity cache refresh failed: key=%s, err=%v", cacheKey, err))
		}
		return
	}

	versionEpoch, versionSeq := nextChannelAffinityVersion()
	replacement := channelAffinityState{
		ChannelID:     channelID,
		VersionEpoch:  versionEpoch,
		VersionSeq:    versionSeq,
		NextProbeAt:   now.Add(channelAffinityProbeInterval(setting)).UnixMilli(),
		IdleExpiresAt: idleExpiresAt,
	}
	if _, err := switchChannelAffinityState(cacheKey, requestState.State, replacement, ttl); err != nil {
		common.SysError(fmt.Sprintf("channel affinity cache switch failed: key=%s, err=%v", cacheKey, err))
	}
}

// RecordChannelAffinityFRT updates the user-scoped latency state whenever the
// request has a valid per-attempt FRT. It only changes the next affinity
// choice; it never cancels or retries the request that produced the observation.
func RecordChannelAffinityFRT(c *gin.Context, relayInfo *relaycommon.RelayInfo, channelID int) {
	setting := operation_setting.GetChannelAffinitySetting()
	if c == nil || relayInfo == nil || setting == nil || !setting.Enabled || !setting.FRTOptimizationEnabled || channelID <= 0 {
		return
	}
	if c.GetInt("id") <= 0 {
		return
	}
	requestState, ok := getChannelAffinityRequestState(c)
	if !ok || !requestState.Found {
		return
	}
	selection, ok := getChannelAffinitySelection(c)
	if !ok || selection.Group == "" {
		return
	}
	if c.GetInt(ginKeyChannelAffinityProbeID) > 0 || c.GetInt("channel_id") != channelID {
		return
	}
	frtMs, ok := relayInfo.FRTMilliseconds()
	if !ok || frtMs < 0 {
		return
	}
	meta, ok := getChannelAffinityMeta(c)
	if !ok || meta.UsingGroup == "" || meta.ModelName == "" {
		return
	}

	observeOnly := false
	if relayInfo.RetryIndex != 0 {
		// A retry success is useful FRT evidence, but it must not directly
		// replace the current affinity target. Only retain observations from
		// the same group and priority so fallback traffic cannot bleed across
		// scheduling tiers.
		retryChannel, channelErr := model.CacheGetChannel(channelID)
		if channelErr != nil || retryChannel == nil || retryChannel.GetPriority() != selection.Priority ||
			!model.IsChannelEnabledForGroupModel(selection.Group, meta.ModelName, channelID) {
			return
		}
		observeOnly = true
	} else if requestState.State.ChannelID != channelID {
		return
	}

	recordChannelAffinityFRTState(c, setting, meta, selection, channelID, frtMs, requestState.State, true, observeOnly)
}

func recordChannelAffinityFRTState(
	c *gin.Context,
	setting *operation_setting.ChannelAffinitySetting,
	meta channelAffinityMeta,
	selection channelAffinitySelection,
	channelID int,
	frtMs int64,
	expected channelAffinityState,
	allowRetry bool,
	observeOnly bool,
) {
	now := time.Now()
	frt := cloneChannelAffinityFRTState(expected.FRT)
	if frt == nil || frt.Group != selection.Group || frt.Priority != selection.Priority {
		frt = &channelAffinityFRTState{Group: selection.Group, Priority: selection.Priority}
	}
	frt.Channels = pruneChannelAffinityFRTScores(frt.Channels, now)
	if frt.ProbeCooldownUntil > 0 && frt.ProbeCooldownUntil <= now.UnixMilli() {
		frt.ProbeCooldownChannelID = 0
		frt.ProbeCooldownUntil = 0
	}
	score := upsertChannelAffinityFRTScore(frt, channelID)
	threshold := channelAffinityFRTDynamicThreshold(score.BaselineFRTMs)
	slow := float64(frtMs) >= channelAffinityFRTExplosionMs || float64(frtMs) > threshold
	updateChannelAffinityFRTScore(score, float64(frtMs), slow, now)

	if observeOnly {
		// Retry observations update only the candidate score. Keep the
		// current affinity target and its state-machine counters unchanged.
	} else if frt.ProbeCooldownUntil > now.UnixMilli() {
		frt.ProbeCount = 0
		frt.VisitedChannelIDs = nil
		if !slow {
			frt.ProbeCooldownChannelID = 0
			frt.ProbeCooldownUntil = 0
		}
	} else if slow {
		frt.ProbeCount++
		frt.VisitedChannelIDs = appendUniqueChannelID(frt.VisitedChannelIDs, channelID)
	} else {
		frt.ProbeCount = 0
		frt.VisitedChannelIDs = nil
		frt.ProbeCooldownChannelID = 0
		frt.ProbeCooldownUntil = 0
	}

	toChannelID := expected.ChannelID
	if !observeOnly {
		toChannelID = channelID
	}
	frtEvent := "fast"
	if slow {
		frtEvent = "slow"
	}
	if !observeOnly && slow && frt.ProbeCount >= channelAffinityFRTProbeCount(setting) && frt.ProbeCooldownUntil <= now.UnixMilli() {
		candidates, err := model.GetSatisfiedChannelsAtPriority(selection.Group, meta.ModelName, frt.Priority, meta.RequestPath)
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity frt candidate lookup failed: group=%s, model=%s, priority=%d, err=%v", selection.Group, meta.ModelName, frt.Priority, err))
		} else {
			target, allVisited := chooseChannelAffinityFRTTarget(candidates, frt, now, channelID)
			if target != nil {
				toChannelID = target.Id
				if allVisited {
					frtEvent = "probe_cooldown"
					frt.ProbeCooldownChannelID = target.Id
					cooldownSeconds := channelAffinityFRTProbeCooldownSeconds(setting)
					frt.ProbeCooldownUntil = now.Add(time.Duration(cooldownSeconds) * time.Second).UnixMilli()
				} else {
					frtEvent = "switched"
				}
				frt.ProbeCount = 0
			}
		}
	}

	frtInfo := map[string]interface{}{
		"event":                frtEvent,
		"frt_ms":               frtMs,
		"baseline_frt_ms":      score.BaselineFRTMs,
		"peak_score_ms":        score.PeakScoreMs,
		"threshold_ms":         threshold,
		"probe_count":          frt.ProbeCount,
		"from_channel_id":      channelID,
		"to_channel_id":        toChannelID,
		"probe_cooldown_until": frt.ProbeCooldownUntil,
	}
	if observeOnly {
		frtInfo["event"] = "retry_observation"
	}

	ttlSeconds := meta.TTLSeconds
	if ttlSeconds <= 0 {
		ttlSeconds = setting.DefaultTTLSeconds
	}
	if ttlSeconds <= 0 {
		ttlSeconds = 3600
	}
	replacement := expected
	if !observeOnly {
		replacement.ChannelID = toChannelID
	}
	replacement.FRT = frt
	replacement.IdleExpiresAt = now.Add(time.Duration(ttlSeconds) * time.Second).UnixMilli()
	versionEpoch, versionSeq := nextChannelAffinityVersion()
	replacement.VersionEpoch = versionEpoch
	replacement.VersionSeq = versionSeq
	updated, err := switchChannelAffinityState(meta.CacheKey, expected, replacement, time.Duration(ttlSeconds)*time.Second)
	if err != nil {
		frtInfo["event"] = "state_update_error"
		frtInfo["to_channel_id"] = channelID
		setChannelAffinityFRTAdminInfo(c, frtInfo)
		common.SysError(fmt.Sprintf("channel affinity frt state update failed: key=%s, err=%v", meta.CacheKey, err))
		return
	}
	if updated {
		setChannelAffinityFRTAdminInfo(c, frtInfo)
		return
	}
	if allowRetry {
		latest, found, getErr := getChannelAffinityCache().Get(meta.CacheKey)
		if getErr != nil {
			common.SysError(fmt.Sprintf("channel affinity frt state reload failed: key=%s, err=%v", meta.CacheKey, getErr))
		} else if found && (observeOnly || latest.ChannelID == channelID) && !sameChannelAffinityVersion(latest, expected) {
			recordChannelAffinityFRTState(c, setting, meta, selection, channelID, frtMs, latest, false, observeOnly)
			return
		}
	}
	frtInfo["event"] = "cas_conflict"
	frtInfo["to_channel_id"] = channelID
	setChannelAffinityFRTAdminInfo(c, frtInfo)
}

func channelAffinityFRTProbeCount(setting *operation_setting.ChannelAffinitySetting) int {
	if setting == nil || setting.FRTProbeCount < 1 {
		return channelAffinityFRTSlowCountThreshold
	}
	if setting.FRTProbeCount > 10 {
		return 10
	}
	return setting.FRTProbeCount
}

func channelAffinityFRTProbeCooldownSeconds(setting *operation_setting.ChannelAffinitySetting) int {
	if setting == nil || setting.FRTProbeCooldownSeconds < 1 {
		return int(channelAffinityFRTProbeCooldown / time.Second)
	}
	if setting.FRTProbeCooldownSeconds > 3600 {
		return 3600
	}
	return setting.FRTProbeCooldownSeconds
}

func channelAffinityFRTDynamicThreshold(baselineFRTMs float64) float64 {
	if baselineFRTMs <= 0 {
		return channelAffinityFRTAcceptableMs
	}
	baselineFRTMs = math.Min(baselineFRTMs, channelAffinityFRTAcceptableMs)
	return math.Min(channelAffinityFRTMaxThresholdMs, math.Max(channelAffinityFRTAcceptableMs, baselineFRTMs*channelAffinityFRTSlowMultiplier))
}

func cloneChannelAffinityFRTState(source *channelAffinityFRTState) *channelAffinityFRTState {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.VisitedChannelIDs = append([]int(nil), source.VisitedChannelIDs...)
	cloned.Channels = make([]channelAffinityFRTChannelScore, len(source.Channels))
	for i, score := range source.Channels {
		cloned.Channels[i] = score
		cloned.Channels[i].RecentFRTMs = append([]float64(nil), score.RecentFRTMs...)
	}
	return &cloned
}

func pruneChannelAffinityFRTScores(scores []channelAffinityFRTChannelScore, now time.Time) []channelAffinityFRTChannelScore {
	cutoff := now.Add(-channelAffinityFRTStateTTL).UnixMilli()
	result := make([]channelAffinityFRTChannelScore, 0, len(scores))
	for _, score := range scores {
		if score.ChannelID > 0 && score.LastObservedAt >= cutoff {
			result = append(result, score)
		}
	}
	if len(result) <= maxChannelAffinityFRTChannels {
		return result
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastObservedAt > result[j].LastObservedAt })
	return result[:maxChannelAffinityFRTChannels]
}

func upsertChannelAffinityFRTScore(state *channelAffinityFRTState, channelID int) *channelAffinityFRTChannelScore {
	for i := range state.Channels {
		if state.Channels[i].ChannelID == channelID {
			return &state.Channels[i]
		}
	}
	if len(state.Channels) >= maxChannelAffinityFRTChannels {
		sort.Slice(state.Channels, func(i, j int) bool { return state.Channels[i].LastObservedAt < state.Channels[j].LastObservedAt })
		state.Channels = state.Channels[1:]
	}
	state.Channels = append(state.Channels, channelAffinityFRTChannelScore{ChannelID: channelID})
	return &state.Channels[len(state.Channels)-1]
}

func updateChannelAffinityFRTScore(score *channelAffinityFRTChannelScore, frtMs float64, slow bool, now time.Time) {
	if !slow && frtMs <= channelAffinityFRTAcceptableMs {
		if score.LastObservedAt == 0 || score.BaselineFRTMs <= 0 {
			score.BaselineFRTMs = frtMs
		} else {
			score.BaselineFRTMs = score.BaselineFRTMs*0.8 + frtMs*0.2
		}
	}
	score.RecentFRTMs = append(score.RecentFRTMs, frtMs)
	if len(score.RecentFRTMs) > channelAffinityFRTRecentSampleLimit {
		score.RecentFRTMs = score.RecentFRTMs[len(score.RecentFRTMs)-channelAffinityFRTRecentSampleLimit:]
	}
	peak := score.PeakScoreMs
	if peak > 0 && score.LastObservedAt > 0 {
		elapsed := time.Duration(now.UnixMilli()-score.LastObservedAt) * time.Millisecond
		if elapsed > 0 {
			peak *= math.Exp(-float64(elapsed) / float64(channelAffinityFRTPeakDecay))
		}
	}
	if frtMs > peak {
		peak = frtMs
	}
	score.PeakScoreMs = peak
	score.LastObservedAt = now.UnixMilli()
}

func channelAffinityFRTRecentRoutingScore(state *channelAffinityFRTState, channelID int, now time.Time) float64 {
	for _, score := range state.Channels {
		if score.ChannelID != channelID || len(score.RecentFRTMs) == 0 {
			continue
		}
		samples := append([]float64(nil), score.RecentFRTMs...)
		sort.Float64s(samples)
		middle := len(samples) / 2
		if len(samples)%2 == 1 {
			return samples[middle]
		}
		return (samples[middle-1] + samples[middle]) / 2
	}
	return channelAffinityFRTRoutingScore(state, channelID, now)
}

func channelAffinityFRTRoutingScore(state *channelAffinityFRTState, channelID int, now time.Time) float64 {
	for _, score := range state.Channels {
		if score.ChannelID != channelID {
			continue
		}
		peak := score.PeakScoreMs
		if peak > 0 && score.LastObservedAt > 0 {
			elapsed := time.Duration(now.UnixMilli()-score.LastObservedAt) * time.Millisecond
			if elapsed > 0 {
				peak *= math.Exp(-float64(elapsed) / float64(channelAffinityFRTPeakDecay))
			}
		}
		baseline := score.BaselineFRTMs
		if baseline <= 0 {
			baseline = channelAffinityFRTColdStartMs
		}
		if peak > baseline {
			return peak
		}
		return baseline
	}
	return channelAffinityFRTColdStartMs
}

type channelAffinityFRTScoredChannel struct {
	channel *model.Channel
	score   float64
}

func chooseChannelAffinityFRTTarget(candidates []*model.Channel, state *channelAffinityFRTState, now time.Time, currentID int) (*model.Channel, bool) {
	if len(candidates) == 0 {
		return nil, true
	}
	visited := make(map[int]struct{}, len(state.VisitedChannelIDs))
	for _, id := range state.VisitedChannelIDs {
		visited[id] = struct{}{}
	}
	available := make([]channelAffinityFRTScoredChannel, 0, len(candidates))
	all := make([]channelAffinityFRTScoredChannel, 0, len(candidates))
	for _, channel := range candidates {
		if channel == nil {
			continue
		}
		item := channelAffinityFRTScoredChannel{channel: channel, score: channelAffinityFRTRoutingScore(state, channel.Id, now)}
		all = append(all, item)
		if channel.Id != currentID {
			if _, seen := visited[channel.Id]; !seen {
				available = append(available, item)
			}
		}
	}
	if len(available) == 0 {
		if len(all) == 0 {
			return nil, true
		}
		for i := range all {
			all[i].score = channelAffinityFRTRecentRoutingScore(state, all[i].channel.Id, now)
		}
		return selectLowestScoreChannel(all), true
	}
	return selectLowestScoreChannel(available), false
}

func selectLowestScoreChannel(candidates []channelAffinityFRTScoredChannel) *model.Channel {
	if len(candidates) == 0 {
		return nil
	}
	lowest := candidates[0].score
	for _, candidate := range candidates[1:] {
		if candidate.score < lowest {
			lowest = candidate.score
		}
	}
	tied := make([]*model.Channel, 0, len(candidates))
	for _, candidate := range candidates {
		if math.Abs(candidate.score-lowest) < 0.001 {
			tied = append(tied, candidate.channel)
		}
	}
	if len(tied) == 1 {
		return tied[0]
	}
	weightSum := 0
	for _, channel := range tied {
		weightSum += channel.GetWeight()
	}
	if weightSum == 0 {
		return tied[rand.Intn(len(tied))]
	}
	choice := rand.Intn(weightSum)
	for _, channel := range tied {
		choice -= channel.GetWeight()
		if choice < 0 {
			return channel
		}
	}
	return tied[len(tied)-1]
}

func appendUniqueChannelID(ids []int, id int) []int {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func setChannelAffinityFRTAdminInfo(c *gin.Context, frtInfo map[string]interface{}) {
	if c == nil || len(frtInfo) == 0 {
		return
	}
	if anyInfo, ok := c.Get(ginKeyChannelAffinityLogInfo); ok {
		if info, ok := anyInfo.(map[string]interface{}); ok {
			info["frt_optimization"] = frtInfo
			c.Set(ginKeyChannelAffinityLogInfo, info)
			return
		}
	}
	c.Set(ginKeyChannelAffinityLogInfo, map[string]interface{}{"frt_optimization": frtInfo})
}

type ChannelAffinityUsageCacheStats struct {
	RuleName            string `json:"rule_name"`
	UsingGroup          string `json:"using_group"`
	KeyFingerprint      string `json:"key_fp"`
	CachedTokenRateMode string `json:"cached_token_rate_mode"`

	Hit           int64 `json:"hit"`
	Total         int64 `json:"total"`
	WindowSeconds int64 `json:"window_seconds"`

	PromptTokens         int64 `json:"prompt_tokens"`
	CompletionTokens     int64 `json:"completion_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	CachedTokens         int64 `json:"cached_tokens"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
	LastSeenAt           int64 `json:"last_seen_at"`
}

type ChannelAffinityUsageCacheCounters struct {
	CachedTokenRateMode string `json:"cached_token_rate_mode"`

	Hit           int64 `json:"hit"`
	Total         int64 `json:"total"`
	WindowSeconds int64 `json:"window_seconds"`

	PromptTokens         int64 `json:"prompt_tokens"`
	CompletionTokens     int64 `json:"completion_tokens"`
	TotalTokens          int64 `json:"total_tokens"`
	CachedTokens         int64 `json:"cached_tokens"`
	PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
	LastSeenAt           int64 `json:"last_seen_at"`
}

var channelAffinityUsageCacheStatsLocks [64]sync.Mutex

// ObserveChannelAffinityUsageCacheByRelayFormat records usage cache stats with a stable rate mode derived from relay format.
func ObserveChannelAffinityUsageCacheByRelayFormat(c *gin.Context, usage *dto.Usage, relayFormat types.RelayFormat) {
	ObserveChannelAffinityUsageCacheFromContext(c, usage, cachedTokenRateModeByRelayFormat(relayFormat))
}

func ObserveChannelAffinityUsageCacheFromContext(c *gin.Context, usage *dto.Usage, cachedTokenRateMode string) {
	statsCtx, ok := GetChannelAffinityStatsContext(c)
	if !ok {
		return
	}
	observeChannelAffinityUsageCache(statsCtx, usage, cachedTokenRateMode)
}

func GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFp string) ChannelAffinityUsageCacheStats {
	ruleName = strings.TrimSpace(ruleName)
	usingGroup = strings.TrimSpace(usingGroup)
	keyFp = strings.TrimSpace(keyFp)

	entryKey := channelAffinityUsageCacheEntryKey(ruleName, usingGroup, keyFp)
	if entryKey == "" {
		return ChannelAffinityUsageCacheStats{
			RuleName:       ruleName,
			UsingGroup:     usingGroup,
			KeyFingerprint: keyFp,
		}
	}

	cache := getChannelAffinityUsageCacheStatsCache()
	v, found, err := cache.Get(entryKey)
	if err != nil || !found {
		return ChannelAffinityUsageCacheStats{
			RuleName:       ruleName,
			UsingGroup:     usingGroup,
			KeyFingerprint: keyFp,
		}
	}
	return ChannelAffinityUsageCacheStats{
		CachedTokenRateMode:  v.CachedTokenRateMode,
		RuleName:             ruleName,
		UsingGroup:           usingGroup,
		KeyFingerprint:       keyFp,
		Hit:                  v.Hit,
		Total:                v.Total,
		WindowSeconds:        v.WindowSeconds,
		PromptTokens:         v.PromptTokens,
		CompletionTokens:     v.CompletionTokens,
		TotalTokens:          v.TotalTokens,
		CachedTokens:         v.CachedTokens,
		PromptCacheHitTokens: v.PromptCacheHitTokens,
		LastSeenAt:           v.LastSeenAt,
	}
}

func observeChannelAffinityUsageCache(statsCtx ChannelAffinityStatsContext, usage *dto.Usage, cachedTokenRateMode string) {
	entryKey := channelAffinityUsageCacheEntryKey(statsCtx.RuleName, statsCtx.UsingGroup, statsCtx.KeyFingerprint)
	if entryKey == "" {
		return
	}

	windowSeconds := statsCtx.TTLSeconds
	if windowSeconds <= 0 {
		return
	}

	cache := getChannelAffinityUsageCacheStatsCache()
	ttl := time.Duration(windowSeconds) * time.Second

	lock := channelAffinityUsageCacheStatsLock(entryKey)
	lock.Lock()
	defer lock.Unlock()

	prev, found, err := cache.Get(entryKey)
	if err != nil {
		return
	}
	next := prev
	if !found {
		next = ChannelAffinityUsageCacheCounters{}
	}
	currentMode := normalizeCachedTokenRateMode(cachedTokenRateMode)
	if currentMode != "" {
		if next.CachedTokenRateMode == "" {
			next.CachedTokenRateMode = currentMode
		} else if next.CachedTokenRateMode != currentMode && next.CachedTokenRateMode != cacheTokenRateModeMixed {
			next.CachedTokenRateMode = cacheTokenRateModeMixed
		}
	}
	next.Total++
	hit, cachedTokens, promptCacheHitTokens := usageCacheSignals(usage)
	if hit {
		next.Hit++
	}
	next.WindowSeconds = windowSeconds
	next.LastSeenAt = time.Now().Unix()
	next.CachedTokens += cachedTokens
	next.PromptCacheHitTokens += promptCacheHitTokens
	next.PromptTokens += int64(usagePromptTokens(usage))
	next.CompletionTokens += int64(usageCompletionTokens(usage))
	next.TotalTokens += int64(usageTotalTokens(usage))
	_ = cache.SetWithTTL(entryKey, next, ttl)
}

func normalizeCachedTokenRateMode(mode string) string {
	switch mode {
	case cacheTokenRateModeCachedOverPrompt:
		return cacheTokenRateModeCachedOverPrompt
	case cacheTokenRateModeCachedOverPromptPlusCached:
		return cacheTokenRateModeCachedOverPromptPlusCached
	case cacheTokenRateModeMixed:
		return cacheTokenRateModeMixed
	default:
		return ""
	}
}

func cachedTokenRateModeByRelayFormat(relayFormat types.RelayFormat) string {
	switch relayFormat {
	case types.RelayFormatOpenAI, types.RelayFormatOpenAIResponses, types.RelayFormatOpenAIResponsesCompaction:
		return cacheTokenRateModeCachedOverPrompt
	case types.RelayFormatClaude:
		return cacheTokenRateModeCachedOverPromptPlusCached
	default:
		return ""
	}
}

func channelAffinityUsageCacheEntryKey(ruleName, usingGroup, keyFp string) string {
	ruleName = strings.TrimSpace(ruleName)
	usingGroup = strings.TrimSpace(usingGroup)
	keyFp = strings.TrimSpace(keyFp)
	if ruleName == "" || keyFp == "" {
		return ""
	}
	return ruleName + "\n" + usingGroup + "\n" + keyFp
}

func usageCacheSignals(usage *dto.Usage) (hit bool, cachedTokens int64, promptCacheHitTokens int64) {
	if usage == nil {
		return false, 0, 0
	}

	cached := int64(0)
	if usage.PromptTokensDetails.CachedTokens > 0 {
		cached = int64(usage.PromptTokensDetails.CachedTokens)
	} else if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
		cached = int64(usage.InputTokensDetails.CachedTokens)
	}
	pcht := int64(0)
	if usage.PromptCacheHitTokens > 0 {
		pcht = int64(usage.PromptCacheHitTokens)
	}
	return cached > 0 || pcht > 0, cached, pcht
}

func usagePromptTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.PromptTokens > 0 {
		return usage.PromptTokens
	}
	return usage.InputTokens
}

func usageCompletionTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.CompletionTokens > 0 {
		return usage.CompletionTokens
	}
	return usage.OutputTokens
}

func usageTotalTokens(usage *dto.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	pt := usagePromptTokens(usage)
	ct := usageCompletionTokens(usage)
	if pt > 0 || ct > 0 {
		return pt + ct
	}
	return 0
}

func getChannelAffinityUsageCacheStatsCache() *cachex.HybridCache[ChannelAffinityUsageCacheCounters] {
	channelAffinityUsageCacheStatsOnce.Do(func() {
		setting := operation_setting.GetChannelAffinitySetting()
		capacity := 100_000
		defaultTTLSeconds := 3600
		if setting != nil {
			if setting.MaxEntries > 0 {
				capacity = setting.MaxEntries
			}
			if setting.DefaultTTLSeconds > 0 {
				defaultTTLSeconds = setting.DefaultTTLSeconds
			}
		}

		channelAffinityUsageCacheStatsCache = cachex.NewHybridCache[ChannelAffinityUsageCacheCounters](cachex.HybridCacheConfig[ChannelAffinityUsageCacheCounters]{
			Namespace: cachex.Namespace(channelAffinityUsageCacheStatsNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[ChannelAffinityUsageCacheCounters]{},
			Memory: func() *hot.HotCache[string, ChannelAffinityUsageCacheCounters] {
				return hot.NewHotCache[string, ChannelAffinityUsageCacheCounters](hot.LRU, capacity).
					WithTTL(time.Duration(defaultTTLSeconds) * time.Second).
					WithJanitor().
					Build()
			},
		})
	})
	return channelAffinityUsageCacheStatsCache
}

func channelAffinityUsageCacheStatsLock(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	idx := h.Sum32() % uint32(len(channelAffinityUsageCacheStatsLocks))
	return &channelAffinityUsageCacheStatsLocks[idx]
}
