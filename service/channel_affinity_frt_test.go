package service

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFRTScore(channelID int, values []float64, now time.Time) channelAffinityFRTChannelScore {
	samples := make([]channelAffinityFRTSample, 0, len(values))
	for _, value := range values {
		samples = append(samples, channelAffinityFRTSample{FRTMs: value, ObservedAt: now.UnixMilli()})
	}
	return channelAffinityFRTChannelScore{
		ChannelID:      channelID,
		LastObservedAt: now.UnixMilli(),
		Samples:        samples,
	}
}

func TestChannelAffinityFRTDynamicThresholdV2(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name      string
		values    []float64
		threshold float64
	}{
		{name: "cold start", threshold: 15_000},
		{name: "insufficient samples", values: []float64{2_000, 3_000}, threshold: 15_000},
		{name: "lower bound", values: []float64{4_000, 5_000, 6_000}, threshold: 8_000},
		{name: "dynamic threshold", values: []float64{12_000, 13_000, 14_000}, threshold: 15_000},
		{name: "upper bound", values: []float64{22_000, 23_000, 24_000}, threshold: 15_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			threshold, stats := channelAffinityFRTDynamicThresholdV2(testFRTScore(1, tc.values, now), now)
			assert.InDelta(t, tc.threshold, threshold, 0.01)
			assert.Equal(t, len(tc.values), stats.Samples)
		})
	}
}

func TestChannelAffinityFRTShouldEvaluate(t *testing.T) {
	setting := &operation_setting.ChannelAffinitySetting{FRTProbeCount: 3}
	scope := &channelAffinityFRTScopeState{ConsecutiveSlow: 1}
	assert.False(t, channelAffinityFRTShouldEvaluate(scope, setting))

	scope.ConsecutiveSlow = 2
	assert.False(t, channelAffinityFRTShouldEvaluate(scope, setting))

	scope.ConsecutiveSlow = 3
	assert.True(t, channelAffinityFRTShouldEvaluate(scope, setting))
}

func TestChannelAffinityFRTPendingSwitchIsConsumedOnlyByTarget(t *testing.T) {
	scope := &channelAffinityFRTScopeState{
		PendingSwitch: &channelAffinityFRTPendingSwitch{
			Event:         "switched",
			FromChannelID: 56,
			ToChannelID:   54,
		},
	}

	assert.Nil(t, takeChannelAffinityFRTPendingSwitch(scope, 56))
	require.NotNil(t, scope.PendingSwitch)

	pending := takeChannelAffinityFRTPendingSwitch(scope, 54)
	require.NotNil(t, pending)
	assert.Equal(t, 56, pending.FromChannelID)
	assert.Equal(t, 54, pending.ToChannelID)
	assert.Nil(t, scope.PendingSwitch)
}

func TestChannelAffinityFRTPendingSwitchIsLoggedOnDestinationRequest(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	cacheKey := fmt.Sprintf("test-frt-pending-destination:%d", time.Now().UnixNano())
	cache := getChannelAffinityCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	scope := channelAffinityFRTScope{
		Group:       "default",
		ModelName:   "gpt-5.6-terra",
		RequestPath: "/v1/chat/completions",
		Stream:      true,
	}
	initial := buildChannelAffinityStateForTest(54, time.Minute)
	initial.FRT = &channelAffinityFRTState{Scopes: []channelAffinityFRTScopeState{{
		channelAffinityFRTScope: scope,
		PendingSwitch: &channelAffinityFRTPendingSwitch{
			Event:           "switched",
			FromChannelID:   56,
			ToChannelID:     54,
			FRTMs:           44_200,
			ThresholdMs:     10_000,
			RoutingScoreMs:  44_200,
			ConsecutiveSlow: 3,
		},
	}}}
	require.NoError(t, cache.SetWithTTL(cacheKey, initial, time.Minute))

	meta := channelAffinityMeta{
		CacheKey:    cacheKey,
		TTLSeconds:  60,
		UsingGroup:  "default",
		ModelName:   scope.ModelName,
		RequestPath: scope.RequestPath,
	}
	selection := channelAffinitySelection{Group: "default"}
	setting := &operation_setting.ChannelAffinitySetting{FRTProbeCount: 3, DefaultTTLSeconds: 60}
	ctx := buildChannelAffinityTemplateContextForTest(meta)
	recordChannelAffinityFRTStateV2WithScope(ctx, setting, meta, selection, scope, 54, 2_800, initial, false, true)

	anyInfo, ok := ctx.Get(ginKeyChannelAffinityLogInfo)
	require.True(t, ok)
	info, ok := anyInfo.(map[string]interface{})
	require.True(t, ok)
	frtInfo, ok := info["frt_optimization"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "switched", frtInfo["event"])
	require.Equal(t, 56, frtInfo["from_channel_id"])
	require.Equal(t, 54, frtInfo["to_channel_id"])

	stored, found, err := cache.Get(cacheKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, stored.FRT.Scopes[0].PendingSwitch)

	secondCtx := buildChannelAffinityTemplateContextForTest(meta)
	recordChannelAffinityFRTStateV2WithScope(secondCtx, setting, meta, selection, scope, 54, 2_600, stored, false, true)
	secondAnyInfo, ok := secondCtx.Get(ginKeyChannelAffinityLogInfo)
	require.True(t, ok)
	secondInfo, ok := secondAnyInfo.(map[string]interface{})
	require.True(t, ok)
	secondFRTInfo, ok := secondInfo["frt_optimization"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "fast", secondFRTInfo["event"])
}

func TestChannelAffinityStateCodecPreservesPendingFRTSwitch(t *testing.T) {
	state := buildChannelAffinityStateForTest(54, time.Minute)
	state.FRT = &channelAffinityFRTState{Scopes: []channelAffinityFRTScopeState{{
		channelAffinityFRTScope: channelAffinityFRTScope{
			Group:       "default",
			ModelName:   "gpt-5.6-terra",
			RequestPath: "/v1/chat/completions",
			Stream:      true,
		},
		PendingSwitch: &channelAffinityFRTPendingSwitch{
			Event:           "switched",
			FromChannelID:   56,
			ToChannelID:     54,
			FRTMs:           44_200,
			ThresholdMs:     10_000,
			RoutingScoreMs:  44_200,
			ConsecutiveSlow: 3,
		},
	}}}

	encoded, err := (channelAffinityStateCodec{}).Encode(state)
	require.NoError(t, err)
	decoded, err := (channelAffinityStateCodec{}).Decode(encoded)
	require.NoError(t, err)
	require.NotNil(t, decoded.FRT)
	require.Len(t, decoded.FRT.Scopes, 1)
	require.Equal(t, state.FRT.Scopes[0].PendingSwitch, decoded.FRT.Scopes[0].PendingSwitch)
}

func TestChannelAffinityFRTAdvantageThresholds(t *testing.T) {
	assert.True(t, channelAffinityFRTHasAdvantage(7_500, 10_000, true))
	assert.False(t, channelAffinityFRTHasAdvantage(8_100, 10_000, true))
	assert.True(t, channelAffinityFRTHasAdvantage(6_000, 10_000, false))
	assert.False(t, channelAffinityFRTHasAdvantage(6_100, 10_000, false))
}

func TestChannelAffinityFRTGlobalWindowAcceptsSingleUser(t *testing.T) {
	now := time.Now()
	legacySamples := make([]map[string]any, 0, 3)
	for range 3 {
		legacySamples = append(legacySamples, map[string]any{
			"frt_ms": 2_000, "observed_at": now.UnixMilli(), "source_user_id": 1, "source_affinity_key": "same-session",
		})
	}
	payload, err := common.Marshal(map[string]any{"samples": legacySamples})
	require.NoError(t, err)
	state, err := (channelAffinityFRTGlobalStateCodec{}).Decode(string(payload))
	require.NoError(t, err)
	stats, valid := channelAffinityFRTGlobalWindowScore(state, now.Add(-time.Minute), now)
	require.True(t, valid)
	assert.Equal(t, 3, stats.Samples)
	assert.Equal(t, 2_000.0, stats.ScoreMs)
	state.Samples = state.Samples[:2]
	_, valid = channelAffinityFRTGlobalWindowScore(state, now.Add(-time.Minute), now)
	assert.False(t, valid, "insufficient samples must still be rejected")
	state.Samples = append(state.Samples, channelAffinityFRTGlobalSample{FRTMs: 500, ObservedAt: now.Add(-4 * time.Minute).UnixMilli()})
	_, valid = channelAffinityFRTGlobalWindowScore(state, now.Add(-time.Minute), now)
	assert.False(t, valid, "expired samples must not satisfy the minimum")
	state.Samples[2].ObservedAt = now.Add(time.Second).UnixMilli()
	_, valid = channelAffinityFRTGlobalWindowScore(state, now.Add(-time.Minute), now)
	assert.False(t, valid, "future samples must not satisfy the minimum")

}

func TestChannelAffinityFRTGlobalObservationsShareAcrossGroupButNotRequestPath(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	modelName := fmt.Sprintf("test-global-scope-%d", time.Now().UnixNano())
	sourceScope := channelAffinityFRTScope{
		Group:       "source-group",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Stream:      true,
	}
	targetScope := channelAffinityFRTScope{
		Group:       "target-group",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Stream:      true,
	}
	channelID := 54
	cacheKey := channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(sourceScope), channelID)
	cache := getChannelAffinityFRTGlobalCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	now := time.Now()
	require.NoError(t, recordChannelAffinityFRTGlobalObservation(sourceScope, channelID, 1_500, now))
	require.NoError(t, recordChannelAffinityFRTGlobalObservation(targetScope, channelID, 1_700, now))

	state, found, err := getChannelAffinityFRTGlobalState(targetScope, channelID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, channelAffinityFRTGlobalScope{RequestPath: sourceScope.RequestPath, ModelName: modelName, Stream: true}, state.Scope)
	require.Len(t, state.Samples, 2)
	assert.Equal(t, 1_500.0, state.Samples[0].FRTMs)
	assert.Equal(t, 1_700.0, state.Samples[1].FRTMs)

	nonStreamScope := targetScope
	nonStreamScope.Stream = false
	_, found, err = getChannelAffinityFRTGlobalState(nonStreamScope, channelID)
	require.NoError(t, err)
	assert.False(t, found)
	differentPath := targetScope
	differentPath.RequestPath = "/v1/responses"
	_, found, err = getChannelAffinityFRTGlobalState(differentPath, channelID)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestChooseChannelAffinityFRTGlobalTargetUsesSharedEvidence(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	modelName := fmt.Sprintf("test-global-target-%d", time.Now().UnixNano())
	sourceScope := channelAffinityFRTScope{
		Group:       "source-group",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Stream:      true,
	}
	selectionScope := channelAffinityFRTScope{
		Group:       "selection-group",
		ModelName:   modelName,
		RequestPath: "/v1/chat/completions",
		Stream:      true,
	}
	fastChannelID := 54
	currentChannelID := 58
	cacheKey := channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(sourceScope), fastChannelID)
	cache := getChannelAffinityFRTGlobalCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	now := time.Now()
	for range 8 {
		require.NoError(t, recordChannelAffinityFRTGlobalObservation(
			sourceScope,
			fastChannelID,
			2_000,
			now,
		))
	}

	// Shared evidence is eligible regardless of which user supplied it.
	target := chooseChannelAffinityFRTGlobalTarget(
		selectionScope,
		[]*model.Channel{{Id: fastChannelID}, {Id: currentChannelID}},
		currentChannelID,
		&channelAffinityFRTScopeState{},
		now,
	)
	require.NotNil(t, target)
	assert.Equal(t, fastChannelID, target.Id)

	episode := &channelAffinityFRTScopeState{
		EpisodeVisitedChannel: []int{fastChannelID, currentChannelID},
		EpisodeSlowAt:         map[int]int64{fastChannelID: now.UnixMilli()},
	}
	candidates := []*model.Channel{{Id: fastChannelID}, {Id: currentChannelID}}
	assert.Nil(t, chooseChannelAffinityFRTGlobalTarget(selectionScope, candidates, currentChannelID, episode, now))

	recoveryAt := now.Add(time.Second)
	require.NoError(t, recordChannelAffinityFRTGlobalObservation(sourceScope, fastChannelID, 2_000, recoveryAt))
	target = chooseChannelAffinityFRTGlobalTarget(selectionScope, candidates, currentChannelID, episode, recoveryAt)
	require.NotNil(t, target)
	assert.Equal(t, fastChannelID, target.Id)
}

func TestChannelAffinityFRTObservationScopeKeepsStreamSeparate(t *testing.T) {
	meta := channelAffinityMeta{ModelName: "gpt-5", RequestPath: "/v1/responses"}
	selection := channelAffinitySelection{Group: "default"}
	scope := channelAffinityFRTScopeForObservation(meta, selection, &relaycommon.RelayInfo{IsStream: true})
	assert.True(t, scope.Stream)
	assert.Equal(t, "default", scope.Group)
}

func TestFRTGlobalRoutingRegression(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		frtA                                    float64
		expired, switching, visitedAll, allSlow bool
		want                                    int
	}{
		{"initial rejects slow despite personal fast", 30_000, false, false, false, false, 182},
		{"initial prefers fast over higher priority unknown", 2_000, false, false, false, false, 165},
		{"initial expired evidence uses priority", 30_000, true, false, false, false, 165},
		{"switch excludes high weight slow", 30_000, false, true, false, false, 129},
		{"switch retains local slow after global expiry", 30_000, true, true, false, false, 129},
		{"switch allows fresh recovery", 2_000, false, true, false, false, 165},
		{"all slow probes remaining channel before fallback", 30_000, false, true, false, true, 129},
		{"all tried allows best fallback", 30_000, false, true, true, true, 129},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupChannelSelectAutoGroupsTest(t)
			oldRedis := common.RedisEnabled
			common.RedisEnabled = false
			setting := operation_setting.GetChannelAffinitySetting()
			oldSetting := *setting
			setting.Enabled, setting.FRTOptimizationEnabled, setting.FRTProbeCount = true, true, 2
			t.Cleanup(func() { common.RedisEnabled = oldRedis; *setting = oldSetting })
			for i, id := range []int{165, 182, 129} {
				createChannelSelectAutoGroupsChannel(t, db, id, "default", t.Name())
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Update("priority", 100-i).Error)
			}
			if tc.frtA == 2_000 && !tc.switching {
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 165).Update("priority", 0).Error)
			}
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 165).Update("weight", 1_000_000).Error)
			model.InitChannelCache()
			meta := channelAffinityMeta{CacheKey: t.Name(), TTLSeconds: 60, ModelName: t.Name(), UsingGroup: "default", RequestPath: "/v1/chat/completions"}
			ctx := buildChannelAffinityTemplateContextForTest(meta)
			ctx.Request = httptest.NewRequest("POST", meta.RequestPath, nil)
			ctx.Set("id", 99)
			ctx.Set("channel_id", 182)
			selection := channelAffinitySelection{Group: "default", Priority: 99}
			ctx.Set(ginKeyChannelAffinitySelection, selection)
			now := time.Now()
			info := &relaycommon.RelayInfo{AttemptStartTime: now.Add(-40 * time.Second), FirstResponseTime: now}
			scope := channelAffinityFRTScopeForObservation(meta, selection, info)
			observed := now
			if tc.expired {
				observed = now.Add(-4 * time.Minute)
			}
			for range 8 {
				require.NoError(t, recordChannelAffinityFRTGlobalObservation(scope, 165, tc.frtA, observed))
			}
			if tc.allSlow {
				for range 8 {
					require.NoError(t, recordChannelAffinityFRTGlobalObservation(scope, 129, 20_000, now))
				}
			}
			t.Cleanup(func() {
				_, _ = getChannelAffinityCache().DeleteMany([]string{meta.CacheKey})
				for _, id := range []int{165, 182, 129} {
					_, _ = getChannelAffinityFRTGlobalCache().DeleteMany([]string{channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), id)})
				}
			})
			if !tc.switching {
				id, found := getPreferredChannelByFRT(ctx, t.Name(), "default")
				require.True(t, found)
				assert.Equal(t, tc.want, id)
				return
			}
			initial := buildChannelAffinityStateForTest(182, time.Minute)
			visited := []int(nil) // stable B has reset the exploration episode
			if tc.visitedAll {
				visited = []int{165, 182, 129}
			}
			channels := []channelAffinityFRTChannelScore{testFRTScore(165, []float64{30_000, 30_000, 30_000}, now), testFRTScore(182, []float64{40_000, 40_000, 40_000}, now)}
			if tc.visitedAll {
				channels = append(channels, testFRTScore(129, []float64{20_000, 20_000, 20_000}, now))
			}
			initial.FRT = &channelAffinityFRTState{Scopes: []channelAffinityFRTScopeState{{channelAffinityFRTScope: scope, Channels: channels, ConsecutiveSlow: 1, EpisodeVisitedChannel: visited}}}
			require.NoError(t, getChannelAffinityCache().SetWithTTL(meta.CacheKey, initial, time.Minute))
			ctx.Set(ginKeyChannelAffinityState, channelAffinityRequestState{State: initial, Found: true})
			RecordChannelAffinityFRT(ctx, info, 182)
			stored, found, err := getChannelAffinityCache().Get(meta.CacheKey)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, tc.want, stored.ChannelID)
			assert.Equal(t, tc.visitedAll, stored.FRT.Scopes[0].CooldownUntil > now.UnixMilli())
		})
	}
}

func TestFRTAnonymousRequestsShareGlobalEvidence(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	modelName := t.Name()
	createChannelSelectAutoGroupsChannel(t, db, 9101, "default", modelName)
	model.InitChannelCache()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	setting := operation_setting.GetChannelAffinitySetting()
	previousSetting := *setting
	setting.Enabled = true
	setting.FRTOptimizationEnabled = true
	t.Cleanup(func() { common.RedisEnabled = previousRedis; *setting = previousSetting })
	meta := channelAffinityMeta{ModelName: modelName, UsingGroup: "default", RequestPath: "/v1/chat/completions"}
	ctx := buildChannelAffinityTemplateContextForTest(meta)
	ctx.Request = httptest.NewRequest("POST", meta.RequestPath, nil)
	ctx.Set("channel_id", 9101)
	ctx.Set(ginKeyChannelAffinitySelection, channelAffinitySelection{Group: "default"})
	now := time.Now()
	info := &relaycommon.RelayInfo{AttemptStartTime: now.Add(-time.Second), FirstResponseTime: now}
	scope := channelAffinityFRTScopeForObservation(meta, channelAffinitySelection{Group: "default"}, info)
	globalKey := channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), 9101)
	t.Cleanup(func() {
		_, _ = getChannelAffinityFRTGlobalCache().DeleteMany([]string{globalKey})
	})
	for range 8 {
		RecordChannelAffinityFRT(ctx, info, 9101)
	}
	global, found, err := getChannelAffinityFRTGlobalState(scope, 9101)
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, global.Samples, 8)
	channelID, selected := getPreferredChannelByFRT(ctx, modelName, "default")
	assert.True(t, selected)
	assert.Equal(t, 9101, channelID)
}

func TestFRTExploresAllChannelsBeforeBestFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		count      int
		globalSlow bool
		localSlow  bool
	}{
		{name: "unknown channels", count: 2},
		{name: "configured consecutive count", count: 3},
		{name: "globally slow channels", count: 2, globalSlow: true},
		{name: "unknown channel with local slow history", count: 2, localSlow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupChannelSelectAutoGroupsTest(t)
			previousRedis := common.RedisEnabled
			common.RedisEnabled = false
			setting := operation_setting.GetChannelAffinitySetting()
			previousSetting := *setting
			setting.Enabled, setting.FRTOptimizationEnabled, setting.FRTProbeCount = true, true, tc.count
			t.Cleanup(func() { common.RedisEnabled = previousRedis; *setting = previousSetting })

			ids := []int{165, 182, 129, 190}
			latencies := []int64{30_000, 20_000, 40_000, 50_000}
			for i, id := range ids {
				createChannelSelectAutoGroupsChannel(t, db, id, "default", t.Name())
				require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Update("priority", 100-i).Error)
			}
			model.InitChannelCache()
			scope := channelAffinityFRTScope{Group: "default", ModelName: t.Name(), RequestPath: "/v1/chat/completions"}
			meta := channelAffinityMeta{CacheKey: t.Name(), TTLSeconds: 60, ModelName: scope.ModelName, RequestPath: scope.RequestPath}
			state := buildChannelAffinityStateForTest(ids[0], time.Minute)
			state.FRT = &channelAffinityFRTState{Scopes: []channelAffinityFRTScopeState{{channelAffinityFRTScope: scope}}}
			if tc.localSlow {
				state.FRT.Scopes[0].Channels = []channelAffinityFRTChannelScore{testFRTScore(ids[3], []float64{50_000}, time.Now())}
			}
			cache := getChannelAffinityCache()
			require.NoError(t, cache.SetWithTTL(meta.CacheKey, state, time.Minute))
			t.Cleanup(func() {
				_, _ = cache.DeleteMany([]string{meta.CacheKey})
				for _, id := range ids {
					_, _ = getChannelAffinityFRTGlobalCache().DeleteMany([]string{channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), id)})
				}
			})
			if tc.globalSlow {
				for i, id := range ids {
					for range 3 {
						require.NoError(t, recordChannelAffinityFRTGlobalObservation(scope, id, float64(latencies[i]), time.Now()))
					}
				}
			}

			for i, id := range ids {
				for attempt := range tc.count {
					require.Equal(t, id, state.ChannelID)
					ctx := buildChannelAffinityTemplateContextForTest(meta)
					ctx.Request = httptest.NewRequest("POST", scope.RequestPath, nil)
					ctx.Set("channel_id", id)
					ctx.Set(ginKeyChannelAffinitySelection, channelAffinitySelection{Group: scope.Group})
					ctx.Set(ginKeyChannelAffinityState, channelAffinityRequestState{State: state, Found: true})
					now := time.Now()
					RecordChannelAffinityFRT(ctx, &relaycommon.RelayInfo{AttemptStartTime: now.Add(-time.Duration(latencies[i]) * time.Millisecond), FirstResponseTime: now}, id)
					stored, found, err := cache.Get(meta.CacheKey)
					require.NoError(t, err)
					require.True(t, found)
					want := id
					allTried := i == len(ids)-1 && attempt == tc.count-1
					if attempt == tc.count-1 {
						if allTried {
							want = ids[1] // B is the best observed channel, not the previous channel C.
						} else {
							want = ids[i+1]
						}
					}
					require.Equal(t, want, stored.ChannelID, "channel %d, slow response %d", id, attempt+1)
					assert.Equal(t, allTried, stored.FRT.Scopes[0].CooldownUntil > now.UnixMilli())
					state = stored
				}
			}
			assert.ElementsMatch(t, ids, state.FRT.Scopes[0].EpisodeVisitedChannel)
			assert.Equal(t, ids[1], state.FRT.Scopes[0].CooldownChannelID)
			cooldownUntil := state.FRT.Scopes[0].CooldownUntil
			for range tc.count {
				ctx := buildChannelAffinityTemplateContextForTest(meta)
				recordChannelAffinityFRTStateV2WithScope(ctx, setting, meta, channelAffinitySelection{Group: scope.Group}, scope, ids[1], latencies[1], state, false, true)
				stored, found, err := cache.Get(meta.CacheKey)
				require.NoError(t, err)
				require.True(t, found)
				assert.Equal(t, ids[1], stored.ChannelID)
				assert.Equal(t, cooldownUntil, stored.FRT.Scopes[0].CooldownUntil)
				state = stored
			}
		})
	}
}

func TestFRTSwitchedChannelStaysAfterFastResponse(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	scope := channelAffinityFRTScope{Group: "default", ModelName: t.Name(), RequestPath: "/v1/chat/completions", Stream: true}
	meta := channelAffinityMeta{CacheKey: t.Name(), TTLSeconds: 60, ModelName: scope.ModelName, RequestPath: scope.RequestPath}
	initial := buildChannelAffinityStateForTest(182, time.Minute)
	initial.FRT = &channelAffinityFRTState{Scopes: []channelAffinityFRTScopeState{{
		channelAffinityFRTScope: scope,
		ConsecutiveSlow:         1,
		PendingSwitch:           &channelAffinityFRTPendingSwitch{Event: "switched", FromChannelID: 165, ToChannelID: 182},
	}}}
	cache := getChannelAffinityCache()
	require.NoError(t, cache.SetWithTTL(meta.CacheKey, initial, time.Minute))
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{meta.CacheKey}) })

	ctx := buildChannelAffinityTemplateContextForTest(meta)
	setting := &operation_setting.ChannelAffinitySetting{FRTProbeCount: 2, DefaultTTLSeconds: 60}
	recordChannelAffinityFRTStateV2WithScope(ctx, setting, meta, channelAffinitySelection{Group: scope.Group}, scope, 182, 2_000, initial, false, true)
	stored, found, err := cache.Get(meta.CacheKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 182, stored.ChannelID)
	require.Len(t, stored.FRT.Scopes, 1)
	assert.Nil(t, stored.FRT.Scopes[0].PendingSwitch)
	assert.Zero(t, stored.FRT.Scopes[0].ConsecutiveSlow)
	assert.Zero(t, stored.FRT.Scopes[0].CooldownUntil)
}

func TestFRTGlobalObservationKeepsNewestSamplesAfterLateArrival(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	scope := channelAffinityFRTScope{ModelName: t.Name(), RequestPath: "/v1/chat/completions", Stream: true}
	key := channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), 165)
	t.Cleanup(func() { _, _ = getChannelAffinityFRTGlobalCache().DeleteMany([]string{key}) })
	now := time.Now()
	for range channelAffinityFRTGlobalSampleLimit {
		require.NoError(t, recordChannelAffinityFRTGlobalObservation(scope, 165, 2_000, now))
	}
	require.NoError(t, recordChannelAffinityFRTGlobalObservation(scope, 165, 30_000, now.Add(-time.Minute)))
	stored, found, err := getChannelAffinityFRTGlobalState(scope, 165)
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, stored.Samples, channelAffinityFRTGlobalSampleLimit)
	for _, sample := range stored.Samples {
		assert.Equal(t, now.UnixMilli(), sample.ObservedAt)
	}
}

func TestFRTRetryUpdatesAutoGroupAfterCrossGroupSelection(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	createChannelSelectAutoGroupsChannel(t, db, 2101, "vip", t.Name())
	createChannelSelectAutoGroupsChannel(t, db, 2102, "default", t.Name())
	model.InitChannelCache()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })

	ctx := buildChannelAffinityTemplateContextForTest(channelAffinityMeta{})
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
	common.SetContextKey(ctx, constant.ContextKeyAutoGroup, "vip")
	common.SetContextKey(ctx, constant.ContextKeyAutoGroupIndex, 0)
	assert.Empty(t, setting.GetAutoGroups())

	param := &RetryParam{Ctx: ctx, TokenGroup: "auto", ModelName: t.Name(), RequestPath: ctx.Request.URL.Path, failedChannelIDs: map[int]struct{}{2101: {}}}
	channel, group, selected, err := param.selectFRTRetryChannel()
	require.NoError(t, err)
	require.True(t, selected)
	require.NotNil(t, channel)
	assert.Equal(t, 2102, channel.Id)
	assert.Equal(t, "default", group)
	assert.Equal(t, "default", common.GetContextKeyString(ctx, constant.ContextKeyAutoGroup))
	index, found := common.GetContextKey(ctx, constant.ContextKeyAutoGroupIndex)
	require.True(t, found)
	assert.Equal(t, 1, index)
}

func TestFRTGlobalCandidatesUseDynamicSlowThreshold(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = previousRedis })
	scope := channelAffinityFRTScope{ModelName: t.Name(), RequestPath: "/v1/chat/completions", Stream: true}
	cache := getChannelAffinityFRTGlobalCache()
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{
			channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), 165),
			channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), 182),
		})
	})
	now := time.Now()
	for range 3 {
		require.NoError(t, recordChannelAffinityFRTGlobalObservation(scope, 165, 10_000, now))
		require.NoError(t, recordChannelAffinityFRTGlobalObservation(scope, 182, 16_000, now))
	}
	fast, unknown, slow := channelAffinityFRTGlobalCandidates(scope, []*model.Channel{{Id: 165}, {Id: 182}}, now)
	require.Len(t, fast, 1)
	assert.Equal(t, 165, fast[0].channel.Id)
	assert.Empty(t, unknown)
	require.Len(t, slow, 1)
	assert.Equal(t, 182, slow[0].channel.Id)
}

func TestFRTFirstSlowRequestSeedsAffinityState(t *testing.T) {
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	setting := operation_setting.GetChannelAffinitySetting()
	previousSetting := *setting
	setting.Enabled, setting.FRTOptimizationEnabled = true, true
	t.Cleanup(func() { common.RedisEnabled = previousRedis; *setting = previousSetting })

	scope := channelAffinityFRTScope{Group: "default", ModelName: t.Name(), RequestPath: "/v1/chat/completions", Stream: true}
	meta := channelAffinityMeta{CacheKey: t.Name(), TTLSeconds: 60, ModelName: scope.ModelName, UsingGroup: scope.Group, RequestPath: scope.RequestPath}
	ctx := buildChannelAffinityTemplateContextForTest(meta)
	ctx.Request = httptest.NewRequest("POST", scope.RequestPath, nil)
	ctx.Set("channel_id", 165)
	ctx.Set(ginKeyChannelAffinitySelection, channelAffinitySelection{Group: scope.Group})
	ctx.Set(ginKeyChannelAffinityState, channelAffinityRequestState{Found: false})
	common.SetContextKey(ctx, constant.ContextKeyIsStream, true)
	cache := getChannelAffinityCache()
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{meta.CacheKey})
		_, _ = getChannelAffinityFRTGlobalCache().DeleteMany([]string{channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(scope), 165)})
	})

	now := time.Now()
	info := &relaycommon.RelayInfo{OriginModelName: scope.ModelName, IsStream: true, AttemptStartTime: now.Add(-20 * time.Second), FirstResponseTime: now}
	RecordChannelAffinityFRT(ctx, info, 165)
	RecordChannelAffinity(ctx, 165)
	stored, found, err := cache.Get(meta.CacheKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 165, stored.ChannelID)
	require.NotNil(t, stored.FRT)
	require.Len(t, stored.FRT.Scopes, 1)
	assert.Equal(t, 1, stored.FRT.Scopes[0].ConsecutiveSlow)
	assert.Equal(t, []int{165}, stored.FRT.Scopes[0].EpisodeVisitedChannel)
	assert.Equal(t, []channelAffinityFRTSample{{FRTMs: 20_000, ObservedAt: now.UnixMilli()}}, stored.FRT.Scopes[0].Channels[0].Samples)
}
