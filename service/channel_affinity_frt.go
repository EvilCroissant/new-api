package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/cachex"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/samber/hot"
	"github.com/tidwall/gjson"
)

const (
	channelAffinityFRTUserCacheNamespace   = "new-api:channel_affinity_frt_user:v2"
	channelAffinityFRTGlobalCacheNamespace = "new-api:channel_affinity_frt_global:v3"
	channelAffinityFRTGlobalSampleLimit    = 64
	channelAffinityFRTGlobalWindow         = time.Minute
	channelAffinityFRTGlobalTTL            = 5 * time.Minute
	channelAffinityFRTGlobalMinSamples     = 8
	channelAffinityFRTGlobalMinUsers       = 2
	channelAffinityFRTCacheLockShards      = 256
)

var (
	channelAffinityFRTUserCacheOnce   sync.Once
	channelAffinityFRTUserCache       *cachex.HybridCache[channelAffinityFRTUserState]
	channelAffinityFRTGlobalCacheOnce sync.Once
	channelAffinityFRTGlobalCache     *cachex.HybridCache[channelAffinityFRTGlobalState]

	channelAffinityFRTUserLocks   [channelAffinityFRTCacheLockShards]sync.Mutex
	channelAffinityFRTGlobalLocks [channelAffinityFRTCacheLockShards]sync.Mutex
)

type channelAffinityFRTUserState struct {
	Scope    channelAffinityFRTScope          `json:"scope"`
	Channels []channelAffinityFRTChannelScore `json:"channels,omitempty"`
}

// channelAffinityFRTGlobalScope 只保留影响物理渠道响应速度的维度。
// Group 和请求路径仍用于筛选候选渠道，但不再拆分同一渠道、模型和流模式的全局 FRT。
type channelAffinityFRTGlobalScope struct {
	ModelName string `json:"model"`
	Stream    bool   `json:"stream"`
}

type channelAffinityFRTGlobalSample struct {
	FRTMs             float64 `json:"frt_ms"`
	ObservedAt        int64   `json:"observed_at"`
	SourceUserID      int     `json:"source_user_id"`
	SourceAffinityKey string  `json:"source_affinity_key,omitempty"`
}

type channelAffinityFRTGlobalState struct {
	Scope     channelAffinityFRTGlobalScope    `json:"scope"`
	ChannelID int                              `json:"channel_id"`
	Samples   []channelAffinityFRTGlobalSample `json:"samples,omitempty"`
}

type channelAffinityFRTUserStateCodec struct{}

func (channelAffinityFRTUserStateCodec) Encode(state channelAffinityFRTUserState) (string, error) {
	payload, err := common.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode channel affinity user frt state: %w", err)
	}
	return string(payload), nil
}

func (channelAffinityFRTUserStateCodec) Decode(value string) (channelAffinityFRTUserState, error) {
	var state channelAffinityFRTUserState
	if strings.TrimSpace(value) == "" {
		return state, fmt.Errorf("empty channel affinity user frt state")
	}
	if err := common.Unmarshal([]byte(value), &state); err != nil {
		return state, fmt.Errorf("decode channel affinity user frt state: %w", err)
	}
	return state, nil
}

type channelAffinityFRTGlobalStateCodec struct{}

func (channelAffinityFRTGlobalStateCodec) Encode(state channelAffinityFRTGlobalState) (string, error) {
	payload, err := common.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode channel affinity global frt state: %w", err)
	}
	return string(payload), nil
}

func (channelAffinityFRTGlobalStateCodec) Decode(value string) (channelAffinityFRTGlobalState, error) {
	var state channelAffinityFRTGlobalState
	if strings.TrimSpace(value) == "" {
		return state, fmt.Errorf("empty channel affinity global frt state")
	}
	if err := common.Unmarshal([]byte(value), &state); err != nil {
		return state, fmt.Errorf("decode channel affinity global frt state: %w", err)
	}
	return state, nil
}

func getChannelAffinityFRTCacheCapacity() int {
	setting := operation_setting.GetChannelAffinitySetting()
	if setting != nil && setting.MaxEntries > 0 {
		return setting.MaxEntries
	}
	return 100_000
}

func getChannelAffinityFRTUserCache() *cachex.HybridCache[channelAffinityFRTUserState] {
	channelAffinityFRTUserCacheOnce.Do(func() {
		channelAffinityFRTUserCache = cachex.NewHybridCache[channelAffinityFRTUserState](cachex.HybridCacheConfig[channelAffinityFRTUserState]{
			Namespace: cachex.Namespace(channelAffinityFRTUserCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: channelAffinityFRTUserStateCodec{},
			Memory: func() *hot.HotCache[string, channelAffinityFRTUserState] {
				return hot.NewHotCache[string, channelAffinityFRTUserState](hot.LRU, getChannelAffinityFRTCacheCapacity()).
					WithTTL(channelAffinityFRTStateTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return channelAffinityFRTUserCache
}

func getChannelAffinityFRTGlobalCache() *cachex.HybridCache[channelAffinityFRTGlobalState] {
	channelAffinityFRTGlobalCacheOnce.Do(func() {
		channelAffinityFRTGlobalCache = cachex.NewHybridCache[channelAffinityFRTGlobalState](cachex.HybridCacheConfig[channelAffinityFRTGlobalState]{
			Namespace: cachex.Namespace(channelAffinityFRTGlobalCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: channelAffinityFRTGlobalStateCodec{},
			Memory: func() *hot.HotCache[string, channelAffinityFRTGlobalState] {
				return hot.NewHotCache[string, channelAffinityFRTGlobalState](hot.LRU, getChannelAffinityFRTCacheCapacity()).
					WithTTL(channelAffinityFRTGlobalTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return channelAffinityFRTGlobalCache
}

func (scope channelAffinityFRTScope) equal(other channelAffinityFRTScope) bool {
	return scope.Group == other.Group &&
		scope.ModelName == other.ModelName &&
		scope.RequestPath == other.RequestPath &&
		scope.Stream == other.Stream
}

func (scope channelAffinityFRTScope) cacheKey() string {
	stream := "0"
	if scope.Stream {
		stream = "1"
	}
	return common.Sha1([]byte(strings.Join([]string{scope.Group, scope.ModelName, scope.RequestPath, stream}, "\x00")))
}

func channelAffinityFRTGlobalScopeFrom(scope channelAffinityFRTScope) channelAffinityFRTGlobalScope {
	return channelAffinityFRTGlobalScope{
		ModelName: scope.ModelName,
		Stream:    scope.Stream,
	}
}

func (scope channelAffinityFRTGlobalScope) equal(other channelAffinityFRTGlobalScope) bool {
	return scope.ModelName == other.ModelName && scope.Stream == other.Stream
}

func (scope channelAffinityFRTGlobalScope) cacheKey() string {
	stream := "0"
	if scope.Stream {
		stream = "1"
	}
	return common.Sha1([]byte(strings.Join([]string{scope.ModelName, stream}, "\x00")))
}

func channelAffinityFRTUserCacheKey(userID int, scope channelAffinityFRTScope) string {
	return strconv.Itoa(userID) + ":" + scope.cacheKey()
}

func channelAffinityFRTGlobalCacheKey(scope channelAffinityFRTGlobalScope, channelID int) string {
	return scope.cacheKey() + ":" + strconv.Itoa(channelID)
}

func channelAffinityFRTLock(cacheKey string, locks *[channelAffinityFRTCacheLockShards]sync.Mutex) *sync.Mutex {
	hash := fnv32(cacheKey)
	return &locks[hash%channelAffinityFRTCacheLockShards]
}

func fnv32(value string) uint32 {
	var hash uint32 = 2166136261
	for i := 0; i < len(value); i++ {
		hash ^= uint32(value[i])
		hash *= 16777619
	}
	return hash
}

func channelAffinityFRTScopeForObservation(meta channelAffinityMeta, selection channelAffinitySelection, relayInfo *relaycommon.RelayInfo) channelAffinityFRTScope {
	return channelAffinityFRTScope{
		Group:       selection.Group,
		ModelName:   meta.ModelName,
		RequestPath: meta.RequestPath,
		Stream:      relayInfo != nil && relayInfo.GetIsStream(),
	}
}

func channelAffinityFRTStreamFromRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return false
	}
	body, err := storage.Bytes()
	if err != nil || !gjson.ValidBytes(body) {
		return false
	}
	value := gjson.GetBytes(body, "stream")
	return value.Exists() && value.Type == gjson.True
}

func channelAffinityFRTScopeForSelection(c *gin.Context, group, modelName string) channelAffinityFRTScope {
	requestPath := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		requestPath = c.Request.URL.Path
	}
	return channelAffinityFRTScope{
		Group:       group,
		ModelName:   modelName,
		RequestPath: requestPath,
		Stream:      channelAffinityFRTStreamFromRequest(c),
	}
}

func channelAffinityFRTMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func channelAffinityFRTP75(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	index := int(math.Ceil(float64(len(ordered))*0.75)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(ordered) {
		index = len(ordered) - 1
	}
	return ordered[index]
}

func channelAffinityFRTRecentSamples(samples []channelAffinityFRTSample, now time.Time) []channelAffinityFRTSample {
	cutoff := now.Add(-channelAffinityFRTSampleTTL).UnixMilli()
	result := make([]channelAffinityFRTSample, 0, len(samples))
	for _, sample := range samples {
		if sample.FRTMs >= 0 && sample.ObservedAt >= cutoff && sample.ObservedAt <= now.UnixMilli() {
			result = append(result, sample)
		}
	}
	return result
}

type channelAffinityFRTScoreStats struct {
	Samples    int
	MedianMs   float64
	MADMs      float64
	Dispersion float64
	ScoreMs    float64
}

func calculateChannelAffinityFRTScoreStats(score channelAffinityFRTChannelScore, now time.Time) (channelAffinityFRTScoreStats, bool) {
	samples := channelAffinityFRTRecentSamples(score.Samples, now)
	if len(samples) == 0 {
		return channelAffinityFRTScoreStats{}, false
	}
	values := make([]float64, 0, len(samples))
	peak := 0.0
	for _, sample := range samples {
		values = append(values, sample.FRTMs)
		age := time.Duration(now.UnixMilli()-sample.ObservedAt) * time.Millisecond
		weighted := sample.FRTMs
		if age > 0 {
			weighted *= math.Exp(-float64(age) / float64(channelAffinityFRTPeakDecay))
		}
		if weighted > peak {
			peak = weighted
		}
	}
	median := channelAffinityFRTMedian(values)
	deviations := make([]float64, 0, len(values))
	for _, value := range values {
		deviations = append(deviations, math.Abs(value-median))
	}
	mad := channelAffinityFRTMedian(deviations)
	dispersion := math.Max(channelAffinityFRTMinimumDispersionMs, channelAffinityFRTMADMultiplier*mad)
	return channelAffinityFRTScoreStats{
		Samples:    len(samples),
		MedianMs:   median,
		MADMs:      mad,
		Dispersion: dispersion,
		ScoreMs:    math.Max(median, peak),
	}, true
}

func channelAffinityFRTDynamicThresholdV2(score channelAffinityFRTChannelScore, now time.Time) (float64, channelAffinityFRTScoreStats) {
	stats, ok := calculateChannelAffinityFRTScoreStats(score, now)
	if !ok || stats.Samples < channelAffinityFRTMinimumSamples {
		return channelAffinityFRTReferenceMaxMs, channelAffinityFRTScoreStats{
			Samples:    stats.Samples,
			MedianMs:   channelAffinityFRTColdStartBaselineMs,
			Dispersion: channelAffinityFRTMinimumDispersionMs,
		}
	}
	// The historical median may tighten the threshold for a previously fast
	// channel, but it must never normalize a chronically slow channel. The
	// absolute red-line remains 10 seconds; MAD only absorbs ordinary jitter
	// below that line.
	threshold := stats.MedianMs + channelAffinityFRTDynamicDispersionFactor*stats.Dispersion
	return math.Max(channelAffinityFRTReferenceMinMs, math.Min(threshold, channelAffinityFRTReferenceMaxMs)), stats
}

func updateChannelAffinityFRTScoreV2(score *channelAffinityFRTChannelScore, frtMs float64, now time.Time) {
	if score == nil {
		return
	}
	score.Samples = channelAffinityFRTRecentSamples(score.Samples, now)
	score.Samples = append(score.Samples, channelAffinityFRTSample{FRTMs: frtMs, ObservedAt: now.UnixMilli()})
	if len(score.Samples) > channelAffinityFRTRecentSampleLimit {
		score.Samples = score.Samples[len(score.Samples)-channelAffinityFRTRecentSampleLimit:]
	}
	score.LastObservedAt = now.UnixMilli()
}

func pruneChannelAffinityFRTScoresV2(scores []channelAffinityFRTChannelScore, now time.Time) []channelAffinityFRTChannelScore {
	stateCutoff := now.Add(-channelAffinityFRTStateTTL).UnixMilli()
	result := make([]channelAffinityFRTChannelScore, 0, len(scores))
	for _, score := range scores {
		if score.ChannelID <= 0 || score.LastObservedAt < stateCutoff {
			continue
		}
		score.Samples = channelAffinityFRTRecentSamples(score.Samples, now)
		if len(score.Samples) == 0 {
			continue
		}
		result = append(result, score)
	}
	if len(result) <= maxChannelAffinityFRTChannels {
		return result
	}
	sort.Slice(result, func(i, j int) bool { return result[i].LastObservedAt > result[j].LastObservedAt })
	return result[:maxChannelAffinityFRTChannels]
}

func upsertChannelAffinityFRTScoreV2(scores *[]channelAffinityFRTChannelScore, channelID int, now time.Time) *channelAffinityFRTChannelScore {
	if scores == nil || channelID <= 0 {
		return nil
	}
	*scores = pruneChannelAffinityFRTScoresV2(*scores, now)
	for i := range *scores {
		if (*scores)[i].ChannelID == channelID {
			return &(*scores)[i]
		}
	}
	if len(*scores) >= maxChannelAffinityFRTChannels {
		sort.Slice(*scores, func(i, j int) bool { return (*scores)[i].LastObservedAt < (*scores)[j].LastObservedAt })
		*scores = (*scores)[1:]
	}
	*scores = append(*scores, channelAffinityFRTChannelScore{ChannelID: channelID})
	return &(*scores)[len(*scores)-1]
}

func cloneChannelAffinityFRTStateV2(source *channelAffinityFRTState) *channelAffinityFRTState {
	if source == nil {
		return &channelAffinityFRTState{}
	}
	cloned := *source
	cloned.Scopes = make([]channelAffinityFRTScopeState, len(source.Scopes))
	for i, scope := range source.Scopes {
		cloned.Scopes[i] = scope
		cloned.Scopes[i].EpisodeVisitedChannel = append([]int(nil), scope.EpisodeVisitedChannel...)
		cloned.Scopes[i].Channels = cloneChannelAffinityFRTScoresV2(scope.Channels)
		if scope.PendingSwitch != nil {
			pending := *scope.PendingSwitch
			cloned.Scopes[i].PendingSwitch = &pending
		}
	}
	return &cloned
}

func cloneChannelAffinityFRTScoresV2(source []channelAffinityFRTChannelScore) []channelAffinityFRTChannelScore {
	cloned := make([]channelAffinityFRTChannelScore, len(source))
	for i, score := range source {
		cloned[i] = score
		cloned[i].Samples = append([]channelAffinityFRTSample(nil), score.Samples...)
	}
	return cloned
}

func upsertChannelAffinityFRTScopeState(state *channelAffinityFRTState, scope channelAffinityFRTScope, now time.Time) *channelAffinityFRTScopeState {
	if state == nil {
		return nil
	}
	for i := range state.Scopes {
		if state.Scopes[i].channelAffinityFRTScope.equal(scope) {
			state.Scopes[i].Channels = pruneChannelAffinityFRTScoresV2(state.Scopes[i].Channels, now)
			return &state.Scopes[i]
		}
	}
	if len(state.Scopes) >= maxChannelAffinityFRTScopes {
		sort.Slice(state.Scopes, func(i, j int) bool { return state.Scopes[i].LastObservedAt < state.Scopes[j].LastObservedAt })
		state.Scopes = state.Scopes[1:]
	}
	state.Scopes = append(state.Scopes, channelAffinityFRTScopeState{channelAffinityFRTScope: scope})
	return &state.Scopes[len(state.Scopes)-1]
}

func findChannelAffinityFRTScopeState(state *channelAffinityFRTState, scope channelAffinityFRTScope) *channelAffinityFRTScopeState {
	if state == nil {
		return nil
	}
	for i := range state.Scopes {
		if state.Scopes[i].channelAffinityFRTScope.equal(scope) {
			return &state.Scopes[i]
		}
	}
	return nil
}

func channelAffinityFRTScoreForChannel(scores []channelAffinityFRTChannelScore, channelID int, now time.Time) (channelAffinityFRTScoreStats, bool) {
	for _, score := range scores {
		if score.ChannelID == channelID {
			return calculateChannelAffinityFRTScoreStats(score, now)
		}
	}
	return channelAffinityFRTScoreStats{}, false
}

type channelAffinityFRTCandidate struct {
	channel *model.Channel
	stats   channelAffinityFRTScoreStats
}

type channelAffinityFRTInitialCandidate struct {
	channel *model.Channel
	score   float64
}

func channelAffinityFRTScoredCandidates(candidates []*model.Channel, scores []channelAffinityFRTChannelScore, now time.Time, minSamples int) []channelAffinityFRTCandidate {
	result := make([]channelAffinityFRTCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		stats, ok := channelAffinityFRTScoreForChannel(scores, candidate.Id, now)
		if !ok || stats.Samples < minSamples {
			continue
		}
		result = append(result, channelAffinityFRTCandidate{channel: candidate, stats: stats})
	}
	return result
}

func channelAffinityFRTHasAdvantage(candidateScore, currentScore float64, samePriority bool) bool {
	if candidateScore <= 0 || currentScore <= 0 || candidateScore >= currentScore {
		return false
	}
	relative := channelAffinityFRTCrossPriorityAdvantage
	if samePriority {
		relative = channelAffinityFRTSamePriorityAdvantage
	}
	return candidateScore <= currentScore-math.Max(channelAffinityFRTMinimumAdvantageMs, currentScore*relative)
}

// getPreferredChannelByFRT only runs for an affinity-key cache miss. It never
// overrides an established affinity entry, and it returns no result unless
// real, fresh FRT evidence is strong enough to justify bypassing the normal
// priority-to-weight selection for this first request.
func getPreferredChannelByFRT(c *gin.Context, modelName string, usingGroup string) (int, bool) {
	if c == nil || c.GetInt("id") <= 0 {
		return 0, false
	}

	groups := []string{usingGroup}
	if usingGroup == "auto" {
		userGroup := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		groups = GetRequestAutoGroups(c, userGroup)
	}
	if len(groups) == 0 {
		return 0, false
	}

	now := time.Now()
	userID := c.GetInt("id")
	for _, group := range groups {
		if strings.TrimSpace(group) == "" {
			continue
		}
		candidates, err := model.GetSatisfiedChannels(group, modelName, channelSelectionFilters(c, requestPathFromContext(c)))
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity initial frt candidate lookup failed: group=%s, model=%s, err=%v", group, modelName, err))
			continue
		}
		if len(candidates) == 0 {
			continue
		}
		if usingGroup == "auto" {
			candidates = channelAffinityFRTFilterAutoGroupCandidates(groups, group, modelName, candidates)
			if len(candidates) == 0 {
				continue
			}
		}

		// Keep the distributor's auto-group ordering: once this is the first
		// usable group, FRT may optimize within it but must not jump to a later
		// group merely because that group has stronger FRT evidence.
		userCandidates := make([]channelAffinityFRTInitialCandidate, 0, len(candidates))
		globalCandidates := make([]channelAffinityFRTInitialCandidate, 0, len(candidates))
		scope := channelAffinityFRTScopeForSelection(c, group, modelName)
		state, found, err := getChannelAffinityFRTUserCache().Get(channelAffinityFRTUserCacheKey(userID, scope))
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity user frt lookup failed: user=%d, group=%s, model=%s, err=%v", userID, group, modelName, err))
		} else if found && state.Scope.equal(scope) {
			for _, candidate := range channelAffinityFRTScoredCandidates(candidates, state.Channels, now, channelAffinityFRTMinimumSamples) {
				userCandidates = append(userCandidates, channelAffinityFRTInitialCandidate{
					channel: candidate.channel,
					score:   candidate.stats.ScoreMs,
				})
			}
		}

		windowStart := now.Add(-channelAffinityFRTGlobalWindow)
		globalScope := channelAffinityFRTGlobalScopeFrom(scope)
		for _, candidate := range candidates {
			state, found, err := getChannelAffinityFRTGlobalState(scope, candidate.Id)
			if err != nil {
				common.SysError(fmt.Sprintf("channel affinity global frt lookup failed: channel=%d, err=%v", candidate.Id, err))
				continue
			}
			if !found || !state.Scope.equal(globalScope) {
				continue
			}
			stats, valid := channelAffinityFRTGlobalWindowScore(state, windowStart, now, userID)
			if !valid {
				continue
			}
			globalCandidates = append(globalCandidates, channelAffinityFRTInitialCandidate{
				channel: candidate,
				score:   stats.ScoreMs,
			})
		}

		if target := chooseChannelAffinityFRTInitialTarget(userCandidates); target != nil {
			return target.channel.Id, true
		}
		if target := chooseChannelAffinityFRTInitialTarget(globalCandidates); target != nil {
			return target.channel.Id, true
		}
		return 0, false
	}
	return 0, false
}

func requestPathFromContext(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return c.Request.URL.Path
}

// channelAffinityFRTFilterAutoGroupCandidates mirrors the distributor's
// ordered auto-group resolution. A channel that is usable in an earlier auto
// group must be scored in that group only, otherwise its scope at selection
// time and observation time could diverge.
func channelAffinityFRTFilterAutoGroupCandidates(autoGroups []string, selectedGroup string, modelName string, candidates []*model.Channel) []*model.Channel {
	filtered := make([]*model.Channel, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		actualGroup := ""
		for _, group := range autoGroups {
			if model.IsChannelEnabledForGroupModel(group, modelName, candidate.Id) {
				actualGroup = group
				break
			}
		}
		if actualGroup == selectedGroup {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func chooseChannelAffinityFRTInitialTarget(candidates []channelAffinityFRTInitialCandidate) *channelAffinityFRTInitialCandidate {
	if len(candidates) == 0 {
		return nil
	}
	best := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].score < best.score {
			best = &candidates[i]
		}
	}
	if best.score < channelAffinityFRTReferenceMinMs {
		return best
	}

	comparisons := 0
	for i := range candidates {
		candidate := &candidates[i]
		if candidate.channel == nil || candidate.channel.Id == best.channel.Id {
			continue
		}
		comparisons++
		if !channelAffinityFRTHasAdvantage(best.score, candidate.score, best.channel.GetPriority() == candidate.channel.GetPriority()) {
			return nil
		}
	}
	if comparisons == 0 {
		return nil
	}
	return best
}

func channelAffinityFRTVisited(ids []int, channelID int) bool {
	for _, id := range ids {
		if id == channelID {
			return true
		}
	}
	return false
}

func channelAffinityFRTChooseLowest(candidates []channelAffinityFRTCandidate) *channelAffinityFRTCandidate {
	if len(candidates) == 0 {
		return nil
	}
	best := &candidates[0]
	for i := 1; i < len(candidates); i++ {
		if candidates[i].stats.ScoreMs < best.stats.ScoreMs {
			best = &candidates[i]
		}
	}
	return best
}

func chooseChannelAffinityFRTTargetV2(candidates []*model.Channel, scopeState *channelAffinityFRTScopeState, currentID int, currentScore float64, currentPriority int64, now time.Time) (*model.Channel, bool) {
	if scopeState == nil {
		return nil, false
	}
	known := channelAffinityFRTScoredCandidates(candidates, scopeState.Channels, now, channelAffinityFRTMinimumSamples)
	if len(known) < 2 {
		return nil, false
	}

	unvisited := make([]channelAffinityFRTCandidate, 0, len(known))
	for _, candidate := range known {
		if candidate.channel.Id == currentID || channelAffinityFRTVisited(scopeState.EpisodeVisitedChannel, candidate.channel.Id) {
			continue
		}
		if channelAffinityFRTHasAdvantage(candidate.stats.ScoreMs, currentScore, candidate.channel.GetPriority() == currentPriority) {
			unvisited = append(unvisited, candidate)
		}
	}
	if target := channelAffinityFRTChooseLowest(unvisited); target != nil {
		return target.channel, false
	}

	allVisited := true
	for _, candidate := range known {
		if !channelAffinityFRTVisited(scopeState.EpisodeVisitedChannel, candidate.channel.Id) {
			allVisited = false
			break
		}
	}
	if !allVisited {
		return nil, false
	}
	if target := channelAffinityFRTChooseLowest(known); target != nil {
		return target.channel, true
	}
	return nil, false
}

func channelAffinityFRTUnvisitedUnknownAtCurrentPriority(candidates []*model.Channel, scopeState *channelAffinityFRTScopeState, currentID int, currentPriority int64, now time.Time) bool {
	if scopeState == nil {
		return false
	}
	for _, candidate := range candidates {
		if candidate == nil || candidate.Id == currentID || candidate.GetPriority() != currentPriority || channelAffinityFRTVisited(scopeState.EpisodeVisitedChannel, candidate.Id) {
			continue
		}
		stats, observed := channelAffinityFRTScoreForChannel(scopeState.Channels, candidate.Id, now)
		if !observed || stats.Samples < channelAffinityFRTMinimumSamples {
			return true
		}
	}
	return false
}

func channelAffinityFRTAllCurrentPriorityCandidatesVisited(candidates []*model.Channel, scopeState *channelAffinityFRTScopeState, currentPriority int64) bool {
	if scopeState == nil {
		return false
	}
	count := 0
	for _, candidate := range candidates {
		if candidate == nil || candidate.GetPriority() != currentPriority {
			continue
		}
		count++
		if !channelAffinityFRTVisited(scopeState.EpisodeVisitedChannel, candidate.Id) {
			return false
		}
	}
	return count > 0
}

func chooseChannelAffinityFRTObservedCurrentPriorityTarget(candidates []*model.Channel, scopeState *channelAffinityFRTScopeState, currentPriority int64, now time.Time) *model.Channel {
	if scopeState == nil {
		return nil
	}
	observed := make([]channelAffinityFRTCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.GetPriority() != currentPriority {
			continue
		}
		stats, ok := channelAffinityFRTScoreForChannel(scopeState.Channels, candidate.Id, now)
		if ok && stats.Samples > 0 {
			observed = append(observed, channelAffinityFRTCandidate{channel: candidate, stats: stats})
		}
	}
	if target := channelAffinityFRTChooseLowest(observed); target != nil {
		return target.channel
	}
	return nil
}

func channelAffinityFRTShouldEvaluate(scopeState *channelAffinityFRTScopeState, setting *operation_setting.ChannelAffinitySetting) bool {
	if scopeState == nil {
		return false
	}
	return scopeState.ConsecutiveSlow >= channelAffinityFRTProbeCount(setting)
}

func channelAffinityFRTIsSwitchEvent(event string) bool {
	switch event {
	case "switched", "switched_exploration", "probe_cooldown", "cooldown_global_switch":
		return true
	default:
		return false
	}
}

func takeChannelAffinityFRTPendingSwitch(scopeState *channelAffinityFRTScopeState, channelID int) *channelAffinityFRTPendingSwitch {
	if scopeState == nil || scopeState.PendingSwitch == nil || scopeState.PendingSwitch.ToChannelID != channelID {
		return nil
	}
	pending := scopeState.PendingSwitch
	scopeState.PendingSwitch = nil
	return pending
}

func channelAffinityFRTPendingSwitchLogInfo(pending *channelAffinityFRTPendingSwitch) map[string]interface{} {
	if pending == nil {
		return nil
	}
	return map[string]interface{}{
		"event":            pending.Event,
		"frt_ms":           pending.FRTMs,
		"threshold_ms":     pending.ThresholdMs,
		"routing_score_ms": pending.RoutingScoreMs,
		"consecutive_slow": pending.ConsecutiveSlow,
		"from_channel_id":  pending.FromChannelID,
		"to_channel_id":    pending.ToChannelID,
	}
}

func recordChannelAffinityFRTStateV2WithScope(c *gin.Context, setting *operation_setting.ChannelAffinitySetting, meta channelAffinityMeta, selection channelAffinitySelection, scope channelAffinityFRTScope, channelID int, frtMs int64, expected channelAffinityState, observeOnly bool, allowRetry bool) {
	now := time.Now()
	frt := cloneChannelAffinityFRTStateV2(expected.FRT)
	scopeState := upsertChannelAffinityFRTScopeState(frt, scope, now)
	if scopeState == nil {
		return
	}
	if scopeState.CooldownUntil > 0 && scopeState.CooldownUntil <= now.UnixMilli() {
		scopeState.CooldownUntil = 0
		scopeState.CooldownChannelID = 0
		scopeState.EpisodeVisitedChannel = nil
		scopeState.StableCount = 0
	}
	pendingSwitch := takeChannelAffinityFRTPendingSwitch(scopeState, channelID)

	score := upsertChannelAffinityFRTScoreV2(&scopeState.Channels, channelID, now)
	if score == nil {
		return
	}
	threshold, beforeStats := channelAffinityFRTDynamicThresholdV2(*score, now)
	slow := float64(frtMs) >= channelAffinityFRTExplosionMs || float64(frtMs) > threshold
	updateChannelAffinityFRTScoreV2(score, float64(frtMs), now)
	scopeState.LastObservedAt = now.UnixMilli()

	toChannelID := expected.ChannelID
	if !observeOnly {
		toChannelID = channelID
	}
	event := "fast"
	if slow {
		event = "slow"
	}
	triggerConsecutiveSlow := scopeState.ConsecutiveSlow

	if !observeOnly {
		if slow {
			scopeState.ConsecutiveSlow++
			scopeState.StableCount = 0
			scopeState.EpisodeVisitedChannel = appendUniqueChannelID(scopeState.EpisodeVisitedChannel, channelID)
		} else {
			scopeState.ConsecutiveSlow = 0
			if len(scopeState.EpisodeVisitedChannel) > 0 {
				scopeState.StableCount++
				if scopeState.StableCount >= channelAffinityFRTStableCountThreshold {
					scopeState.EpisodeVisitedChannel = nil
					scopeState.StableCount = 0
				}
			}
		}

		triggerConsecutiveSlow = scopeState.ConsecutiveSlow
		if slow && channelAffinityFRTShouldEvaluate(scopeState, setting) {
			filters := channelSelectionFilters(c, meta.RequestPath)
			candidates, err := model.GetSatisfiedChannels(selection.Group, meta.ModelName, filters)
			if err != nil {
				common.SysError(fmt.Sprintf("channel affinity frt candidate lookup failed: group=%s, model=%s, err=%v", selection.Group, meta.ModelName, err))
			} else {
				currentStats, hasCurrentStats := channelAffinityFRTScoreForChannel(scopeState.Channels, channelID, now)
				currentScore := float64(frtMs)
				if hasCurrentStats && currentStats.ScoreMs > currentScore {
					currentScore = currentStats.ScoreMs
				}

				if scopeState.CooldownUntil > now.UnixMilli() {
					if target := chooseChannelAffinityFRTGlobalTarget(scope, candidates, channelID, currentScore, selection.Priority, c.GetInt("id"), now); target != nil {
						toChannelID = target.Id
						event = "cooldown_global_switch"
						scopeState.CooldownUntil = 0
						scopeState.CooldownChannelID = 0
						scopeState.ConsecutiveSlow = 0
						scopeState.StableCount = 0
						scopeState.EpisodeVisitedChannel = appendUniqueChannelID(scopeState.EpisodeVisitedChannel, target.Id)
					} else {
						event = "cooldown_hold"
					}
				} else {
					globalTarget := chooseChannelAffinityFRTGlobalTarget(scope, candidates, channelID, currentScore, selection.Priority, c.GetInt("id"), now)
					if globalTarget != nil {
						toChannelID = globalTarget.Id
						scopeState.ConsecutiveSlow = 0
						scopeState.StableCount = 0
						scopeState.EpisodeVisitedChannel = appendUniqueChannelID(scopeState.EpisodeVisitedChannel, globalTarget.Id)
						event = "switched"
					} else {
						target, allVisited := chooseChannelAffinityFRTTargetV2(candidates, scopeState, channelID, currentScore, selection.Priority, now)
						if target != nil && !allVisited {
							toChannelID = target.Id
							scopeState.ConsecutiveSlow = 0
							scopeState.StableCount = 0
							scopeState.EpisodeVisitedChannel = appendUniqueChannelID(scopeState.EpisodeVisitedChannel, target.Id)
							event = "switched"
						} else if channelAffinityFRTUnvisitedUnknownAtCurrentPriority(candidates, scopeState, channelID, selection.Priority, now) {
							skippedChannelIDs := make(map[int]struct{}, len(scopeState.EpisodeVisitedChannel)+1)
							skippedChannelIDs[channelID] = struct{}{}
							for _, visitedID := range scopeState.EpisodeVisitedChannel {
								skippedChannelIDs[visitedID] = struct{}{}
							}
							target, selectErr := model.GetRandomSatisfiedChannelAtPrioritySkippingChannels(selection.Group, meta.ModelName, selection.Priority, filters, skippedChannelIDs)
							if selectErr != nil {
								common.SysError(fmt.Sprintf("channel affinity frt unknown candidate selection failed: group=%s, model=%s, priority=%d, err=%v", selection.Group, meta.ModelName, selection.Priority, selectErr))
							} else if target != nil {
								toChannelID = target.Id
								scopeState.ConsecutiveSlow = 0
								scopeState.StableCount = 0
								scopeState.EpisodeVisitedChannel = appendUniqueChannelID(scopeState.EpisodeVisitedChannel, target.Id)
								event = "switched_exploration"
							}
						} else if target != nil {
							toChannelID = target.Id
							scopeState.ConsecutiveSlow = 0
							scopeState.StableCount = 0
							scopeState.EpisodeVisitedChannel = appendUniqueChannelID(scopeState.EpisodeVisitedChannel, target.Id)
							scopeState.CooldownChannelID = target.Id
							scopeState.CooldownUntil = now.Add(time.Duration(channelAffinityFRTProbeCooldownSeconds(setting)) * time.Second).UnixMilli()
							event = "probe_cooldown"
						} else if channelAffinityFRTAllCurrentPriorityCandidatesVisited(candidates, scopeState, selection.Priority) {
							if target := chooseChannelAffinityFRTObservedCurrentPriorityTarget(candidates, scopeState, selection.Priority, now); target != nil {
								toChannelID = target.Id
								scopeState.ConsecutiveSlow = 0
								scopeState.StableCount = 0
								scopeState.CooldownChannelID = target.Id
								scopeState.CooldownUntil = now.Add(time.Duration(channelAffinityFRTProbeCooldownSeconds(setting)) * time.Second).UnixMilli()
								event = "probe_cooldown"
							}
						}
					}
				}
			}
		}
	}

	afterStats, _ := calculateChannelAffinityFRTScoreStats(*score, now)
	if channelAffinityFRTIsSwitchEvent(event) && toChannelID != channelID {
		scopeState.PendingSwitch = &channelAffinityFRTPendingSwitch{
			Event:           event,
			FromChannelID:   channelID,
			ToChannelID:     toChannelID,
			FRTMs:           frtMs,
			ThresholdMs:     threshold,
			RoutingScoreMs:  afterStats.ScoreMs,
			ConsecutiveSlow: triggerConsecutiveSlow,
		}
	}
	frtInfo := map[string]interface{}{
		"event":                event,
		"frt_ms":               frtMs,
		"threshold_ms":         threshold,
		"median_frt_ms":        afterStats.MedianMs,
		"mad_frt_ms":           afterStats.MADMs,
		"routing_score_ms":     afterStats.ScoreMs,
		"sample_count":         afterStats.Samples,
		"consecutive_slow":     triggerConsecutiveSlow,
		"stable_count":         scopeState.StableCount,
		"from_channel_id":      channelID,
		"to_channel_id":        toChannelID,
		"probe_cooldown_until": scopeState.CooldownUntil,
		"scope": map[string]interface{}{
			"group":        scope.Group,
			"model":        scope.ModelName,
			"request_path": scope.RequestPath,
			"stream":       scope.Stream,
		},
	}
	if beforeStats.Samples < channelAffinityFRTMinimumSamples {
		frtInfo["threshold_source"] = "cold_start"
	} else {
		frtInfo["threshold_source"] = "median_mad"
	}
	if observeOnly {
		frtInfo["event"] = "retry_observation"
	} else if channelAffinityFRTIsSwitchEvent(event) && toChannelID != channelID {
		// The actual route is persisted now, but the visible switch marker is
		// deferred until a successful request reaches the destination channel.
		frtInfo["event"] = "switch_scheduled"
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
		if pendingInfo := channelAffinityFRTPendingSwitchLogInfo(pendingSwitch); pendingInfo != nil {
			setChannelAffinityFRTAdminInfo(c, pendingInfo)
		} else {
			setChannelAffinityFRTAdminInfo(c, frtInfo)
		}
		return
	}
	if allowRetry {
		latest, found, getErr := getChannelAffinityCache().Get(meta.CacheKey)
		if getErr != nil {
			common.SysError(fmt.Sprintf("channel affinity frt state reload failed: key=%s, err=%v", meta.CacheKey, getErr))
		} else if found && (observeOnly || latest.ChannelID == channelID) && !sameChannelAffinityVersion(latest, expected) {
			recordChannelAffinityFRTStateV2WithScope(c, setting, meta, selection, scope, channelID, frtMs, latest, observeOnly, false)
			return
		}
	}
	frtInfo["event"] = "cas_conflict"
	frtInfo["to_channel_id"] = channelID
	setChannelAffinityFRTAdminInfo(c, frtInfo)
}

func recordChannelAffinityFRTUserObservation(userID int, scope channelAffinityFRTScope, channelID int, frtMs float64, now time.Time) error {
	if userID <= 0 || channelID <= 0 {
		return nil
	}
	cacheKey := channelAffinityFRTUserCacheKey(userID, scope)
	cache := getChannelAffinityFRTUserCache()
	update := func(state *channelAffinityFRTUserState) {
		if !state.Scope.equal(scope) {
			*state = channelAffinityFRTUserState{Scope: scope}
		}
		score := upsertChannelAffinityFRTScoreV2(&state.Channels, channelID, now)
		updateChannelAffinityFRTScoreV2(score, frtMs, now)
	}

	if common.RedisEnabled && common.RDB != nil {
		fullKey := cache.FullKey(cacheKey)
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), channelAffinityRedisTimeout)
			err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
				var state channelAffinityFRTUserState
				raw, err := tx.Get(ctx, fullKey).Result()
				if err != nil && !errors.Is(err, redis.Nil) {
					return err
				}
				if err == nil {
					state, err = (channelAffinityFRTUserStateCodec{}).Decode(raw)
					if err != nil {
						return err
					}
				}
				update(&state)
				encoded, err := (channelAffinityFRTUserStateCodec{}).Encode(state)
				if err != nil {
					return err
				}
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					pipe.Set(ctx, fullKey, encoded, channelAffinityFRTStateTTL)
					return nil
				})
				return err
			}, fullKey)
			cancel()
			if !errors.Is(err, redis.TxFailedErr) {
				return err
			}
		}
		return redis.TxFailedErr
	}

	lock := channelAffinityFRTLock(cacheKey, &channelAffinityFRTUserLocks)
	lock.Lock()
	defer lock.Unlock()
	state, found, err := cache.Get(cacheKey)
	if err != nil {
		return err
	}
	if !found {
		state = channelAffinityFRTUserState{Scope: scope}
	}
	update(&state)
	return cache.SetWithTTL(cacheKey, state, channelAffinityFRTStateTTL)
}

func channelAffinityFRTRecentGlobalSamples(samples []channelAffinityFRTGlobalSample, now time.Time) []channelAffinityFRTGlobalSample {
	cutoff := now.Add(-2 * channelAffinityFRTGlobalWindow).UnixMilli()
	result := make([]channelAffinityFRTGlobalSample, 0, len(samples))
	for _, sample := range samples {
		if sample.FRTMs >= 0 && sample.ObservedAt >= cutoff && sample.ObservedAt <= now.UnixMilli() {
			result = append(result, sample)
		}
	}
	return result
}

func recordChannelAffinityFRTGlobalObservation(userID int, affinityKey string, scope channelAffinityFRTScope, channelID int, frtMs float64, now time.Time) error {
	if userID <= 0 || channelID <= 0 {
		return nil
	}
	globalScope := channelAffinityFRTGlobalScopeFrom(scope)
	cacheKey := channelAffinityFRTGlobalCacheKey(globalScope, channelID)
	cache := getChannelAffinityFRTGlobalCache()
	update := func(state *channelAffinityFRTGlobalState) {
		if !state.Scope.equal(globalScope) || state.ChannelID != channelID {
			*state = channelAffinityFRTGlobalState{Scope: globalScope, ChannelID: channelID}
		}
		state.Samples = channelAffinityFRTRecentGlobalSamples(state.Samples, now)
		state.Samples = append(state.Samples, channelAffinityFRTGlobalSample{
			FRTMs:             frtMs,
			ObservedAt:        now.UnixMilli(),
			SourceUserID:      userID,
			SourceAffinityKey: affinityKey,
		})
		if len(state.Samples) > channelAffinityFRTGlobalSampleLimit {
			state.Samples = state.Samples[len(state.Samples)-channelAffinityFRTGlobalSampleLimit:]
		}
	}

	if common.RedisEnabled && common.RDB != nil {
		fullKey := cache.FullKey(cacheKey)
		for attempt := 0; attempt < 3; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), channelAffinityRedisTimeout)
			err := common.RDB.Watch(ctx, func(tx *redis.Tx) error {
				var state channelAffinityFRTGlobalState
				raw, err := tx.Get(ctx, fullKey).Result()
				if err != nil && !errors.Is(err, redis.Nil) {
					return err
				}
				if err == nil {
					state, err = (channelAffinityFRTGlobalStateCodec{}).Decode(raw)
					if err != nil {
						return err
					}
				}
				update(&state)
				encoded, err := (channelAffinityFRTGlobalStateCodec{}).Encode(state)
				if err != nil {
					return err
				}
				_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
					pipe.Set(ctx, fullKey, encoded, channelAffinityFRTGlobalTTL)
					return nil
				})
				return err
			}, fullKey)
			cancel()
			if !errors.Is(err, redis.TxFailedErr) {
				return err
			}
		}
		return redis.TxFailedErr
	}

	lock := channelAffinityFRTLock(cacheKey, &channelAffinityFRTGlobalLocks)
	lock.Lock()
	defer lock.Unlock()
	state, found, err := cache.Get(cacheKey)
	if err != nil {
		return err
	}
	if !found {
		state = channelAffinityFRTGlobalState{Scope: globalScope, ChannelID: channelID}
	}
	update(&state)
	return cache.SetWithTTL(cacheKey, state, channelAffinityFRTGlobalTTL)
}

type channelAffinityFRTGlobalWindowStats struct {
	Samples       int
	Users         int
	DominantShare float64
	P50Ms         float64
	P75Ms         float64
	ScoreMs       float64
}

func channelAffinityFRTGlobalWindowScore(state channelAffinityFRTGlobalState, from, to time.Time, excludedUserID int) (channelAffinityFRTGlobalWindowStats, bool) {
	values := make([]float64, 0, len(state.Samples))
	byUser := make(map[int]int)
	fromMs := from.UnixMilli()
	toMs := to.UnixMilli()
	for _, sample := range state.Samples {
		if sample.ObservedAt <= fromMs || sample.ObservedAt > toMs || sample.SourceUserID == excludedUserID {
			continue
		}
		values = append(values, sample.FRTMs)
		byUser[sample.SourceUserID]++
	}
	if len(values) < channelAffinityFRTGlobalMinSamples || len(byUser) < channelAffinityFRTGlobalMinUsers {
		return channelAffinityFRTGlobalWindowStats{}, false
	}
	dominant := 0
	for _, count := range byUser {
		if count > dominant {
			dominant = count
		}
	}
	dominantShare := float64(dominant) / float64(len(values))
	if dominantShare >= 0.5 {
		return channelAffinityFRTGlobalWindowStats{}, false
	}
	p50 := channelAffinityFRTMedian(values)
	p75 := channelAffinityFRTP75(values)
	return channelAffinityFRTGlobalWindowStats{
		Samples:       len(values),
		Users:         len(byUser),
		DominantShare: dominantShare,
		P50Ms:         p50,
		P75Ms:         p75,
		ScoreMs:       p50 + 0.5*(p75-p50),
	}, true
}

func getChannelAffinityFRTGlobalState(scope channelAffinityFRTScope, channelID int) (channelAffinityFRTGlobalState, bool, error) {
	return getChannelAffinityFRTGlobalCache().Get(channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), channelID))
}

func chooseChannelAffinityFRTGlobalTarget(scope channelAffinityFRTScope, candidates []*model.Channel, currentID int, currentScore float64, currentPriority int64, currentUserID int, now time.Time) *model.Channel {
	currentWindowStart := now.Add(-channelAffinityFRTGlobalWindow)
	globalScope := channelAffinityFRTGlobalScopeFrom(scope)
	eligible := make([]channelAffinityFRTCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil || candidate.Id == currentID {
			continue
		}
		state, found, err := getChannelAffinityFRTGlobalState(scope, candidate.Id)
		if err != nil {
			common.SysError(fmt.Sprintf("channel affinity global frt lookup failed: channel=%d, err=%v", candidate.Id, err))
			continue
		}
		if !found || !state.Scope.equal(globalScope) {
			continue
		}
		currentWindow, currentOK := channelAffinityFRTGlobalWindowScore(state, currentWindowStart, now, currentUserID)
		if !currentOK {
			continue
		}
		score := currentWindow.ScoreMs
		if !channelAffinityFRTHasAdvantage(score, currentScore, candidate.GetPriority() == currentPriority) {
			continue
		}
		eligible = append(eligible, channelAffinityFRTCandidate{channel: candidate, stats: channelAffinityFRTScoreStats{ScoreMs: score}})
	}
	if target := channelAffinityFRTChooseLowest(eligible); target != nil {
		return target.channel
	}
	return nil
}
