package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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
	coldThreshold, coldStats := channelAffinityFRTDynamicThresholdV2(channelAffinityFRTChannelScore{}, now)
	require.Equal(t, channelAffinityFRTReferenceMaxMs, coldThreshold)
	require.Zero(t, coldStats.Samples)

	threshold, stats := channelAffinityFRTDynamicThresholdV2(testFRTScore(1, []float64{4_000, 5_000, 6_000}, now), now)
	require.Equal(t, 3, stats.Samples)
	assert.InDelta(t, 5_000, stats.MedianMs, 0.001)
	assert.InDelta(t, 1_000, stats.MADMs, 0.001)
	assert.InDelta(t, 7_223.9, threshold, 0.01)
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

func TestChooseChannelAffinityFRTTargetV2AvoidsEpisodeOscillation(t *testing.T) {
	now := time.Now()
	channels := []*model.Channel{{Id: 1}, {Id: 2}}
	scope := &channelAffinityFRTScopeState{
		EpisodeVisitedChannel: []int{1},
		Channels: []channelAffinityFRTChannelScore{
			testFRTScore(1, []float64{12_000, 12_000, 12_000}, now),
			testFRTScore(2, []float64{3_000, 3_000, 3_000}, now),
		},
	}

	target, allVisited := chooseChannelAffinityFRTTargetV2(channels, scope, 1, 12_000, 0, now)
	require.NotNil(t, target)
	assert.Equal(t, 2, target.Id)
	assert.False(t, allVisited)

	scope.EpisodeVisitedChannel = appendUniqueChannelID(scope.EpisodeVisitedChannel, target.Id)
	// Channel C obtained its three real observations through unrelated retry
	// traffic after the first A -> B switch; it is now eligible, while A stays
	// blocked for the current episode.
	channels = append(channels, &model.Channel{Id: 3})
	scope.Channels = append(scope.Channels, testFRTScore(3, []float64{2_000, 2_000, 2_000}, now))
	target, allVisited = chooseChannelAffinityFRTTargetV2(channels, scope, 2, 13_000, 0, now)
	require.NotNil(t, target)
	assert.Equal(t, 3, target.Id)
	assert.False(t, allVisited)
}

func TestChooseChannelAffinityFRTTargetV2AllVisitedReturnsFastest(t *testing.T) {
	now := time.Now()
	channels := []*model.Channel{{Id: 1}, {Id: 2}, {Id: 3}}
	scope := &channelAffinityFRTScopeState{
		EpisodeVisitedChannel: []int{1, 2, 3},
		Channels: []channelAffinityFRTChannelScore{
			testFRTScore(1, []float64{12_000, 12_000, 12_000}, now),
			testFRTScore(2, []float64{3_000, 3_000, 3_000}, now),
			testFRTScore(3, []float64{5_000, 5_000, 5_000}, now),
		},
	}

	target, allVisited := chooseChannelAffinityFRTTargetV2(channels, scope, 3, 12_000, 0, now)
	require.NotNil(t, target)
	assert.Equal(t, 2, target.Id)
	assert.True(t, allVisited)
}

func TestChannelAffinityFRTUnvisitedUnknownAtCurrentPriority(t *testing.T) {
	now := time.Now()
	priorityOne := int64(1)
	channels := []*model.Channel{{Id: 1}, {Id: 2}, {Id: 3, Priority: &priorityOne}}
	scope := &channelAffinityFRTScopeState{
		EpisodeVisitedChannel: []int{1},
		Channels: []channelAffinityFRTChannelScore{
			testFRTScore(1, []float64{12_000, 12_000}, now),
			testFRTScore(3, []float64{3_000, 3_000, 3_000}, now),
		},
	}

	assert.True(t, channelAffinityFRTUnvisitedUnknownAtCurrentPriority(channels, scope, 1, 0, now))
	scope.EpisodeVisitedChannel = append(scope.EpisodeVisitedChannel, 2)
	assert.False(t, channelAffinityFRTUnvisitedUnknownAtCurrentPriority(channels, scope, 1, 0, now))
}

func TestChooseChannelAffinityFRTObservedCurrentPriorityTarget(t *testing.T) {
	now := time.Now()
	priorityOne := int64(1)
	channels := []*model.Channel{{Id: 1}, {Id: 2}, {Id: 3, Priority: &priorityOne}}
	scope := &channelAffinityFRTScopeState{Channels: []channelAffinityFRTChannelScore{
		testFRTScore(1, []float64{12_000, 12_000}, now),
		testFRTScore(2, []float64{7_000, 7_000}, now),
		testFRTScore(3, []float64{1_000, 1_000}, now),
	}}

	target := chooseChannelAffinityFRTObservedCurrentPriorityTarget(channels, scope, 0, now)
	require.NotNil(t, target)
	assert.Equal(t, 2, target.Id)
}

func TestChannelAffinityFRTAdvantageThresholds(t *testing.T) {
	assert.True(t, channelAffinityFRTHasAdvantage(7_500, 10_000, true))
	assert.False(t, channelAffinityFRTHasAdvantage(8_100, 10_000, true))
	assert.True(t, channelAffinityFRTHasAdvantage(6_000, 10_000, false))
	assert.False(t, channelAffinityFRTHasAdvantage(6_100, 10_000, false))
}

func TestChooseChannelAffinityFRTInitialTarget(t *testing.T) {
	green := chooseChannelAffinityFRTInitialTarget([]channelAffinityFRTInitialCandidate{{
		channel: &model.Channel{Id: 1},
		score:   4_900,
	}})
	require.NotNil(t, green)
	assert.Equal(t, 1, green.channel.Id)

	confirmed := chooseChannelAffinityFRTInitialTarget([]channelAffinityFRTInitialCandidate{
		{channel: &model.Channel{Id: 1}, score: 6_000},
		{channel: &model.Channel{Id: 2}, score: 9_000},
	})
	require.NotNil(t, confirmed)
	assert.Equal(t, 1, confirmed.channel.Id)

	assert.Nil(t, chooseChannelAffinityFRTInitialTarget([]channelAffinityFRTInitialCandidate{{
		channel: &model.Channel{Id: 1},
		score:   6_000,
	}}))
}

func TestChannelAffinityFRTGlobalWindowRejectsSingleUserDominance(t *testing.T) {
	now := time.Now()
	state := channelAffinityFRTGlobalState{Samples: []channelAffinityFRTGlobalSample{
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 2},
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 2},
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 2},
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 3},
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 3},
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 3},
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 4},
		{FRTMs: 500, ObservedAt: now.UnixMilli(), SourceUserID: 4},
	}}
	stats, valid := channelAffinityFRTGlobalWindowScore(state, now.Add(-time.Minute), now, 1)
	require.True(t, valid)
	assert.Equal(t, 8, stats.Samples)
	assert.Equal(t, 3, stats.Users)
	assert.InDelta(t, 0.375, stats.DominantShare, 0.001)

	state.Samples[0].SourceUserID = 2
	state.Samples[1].SourceUserID = 2
	state.Samples[2].SourceUserID = 2
	state.Samples[3].SourceUserID = 2
	state.Samples[4].SourceUserID = 2
	state.Samples[5].SourceUserID = 3
	state.Samples[6].SourceUserID = 3
	state.Samples[7].SourceUserID = 4
	_, valid = channelAffinityFRTGlobalWindowScore(state, now.Add(-time.Minute), now, 1)
	assert.False(t, valid)
}

func TestChannelAffinityFRTGlobalObservationsShareAcrossGroupAndRequestPath(t *testing.T) {
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
		RequestPath: "/v1/responses",
		Stream:      true,
	}
	channelID := 54
	cacheKey := channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(sourceScope), channelID)
	cache := getChannelAffinityFRTGlobalCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	now := time.Now()
	require.NoError(t, recordChannelAffinityFRTGlobalObservation(2, "source-key", sourceScope, channelID, 1_500, now))
	require.NoError(t, recordChannelAffinityFRTGlobalObservation(3, "target-key", targetScope, channelID, 1_700, now))

	state, found, err := getChannelAffinityFRTGlobalState(targetScope, channelID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, channelAffinityFRTGlobalScope{ModelName: modelName, Stream: true}, state.Scope)
	require.Len(t, state.Samples, 2)
	assert.Equal(t, 1_500.0, state.Samples[0].FRTMs)
	assert.Equal(t, 1_700.0, state.Samples[1].FRTMs)

	nonStreamScope := targetScope
	nonStreamScope.Stream = false
	_, found, err = getChannelAffinityFRTGlobalState(nonStreamScope, channelID)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestChooseChannelAffinityFRTGlobalTargetUsesFreshEvidenceDespitePriorVisit(t *testing.T) {
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
		RequestPath: "/v1/responses",
		Stream:      true,
	}
	fastChannelID := 54
	currentChannelID := 58
	cacheKey := channelAffinityFRTGlobalCacheKey(channelAffinityFRTGlobalScopeFrom(sourceScope), fastChannelID)
	cache := getChannelAffinityFRTGlobalCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	now := time.Now()
	users := []int{2, 2, 2, 3, 3, 3, 4, 4}
	for i, userID := range users {
		require.NoError(t, recordChannelAffinityFRTGlobalObservation(
			userID,
			fmt.Sprintf("affinity-%d", i),
			sourceScope,
			fastChannelID,
			2_000,
			now,
		))
	}

	// 全局证据不受本轮历史访问记录限制：其他用户的新鲜数据确认恢复后，
	// 先前曾经慢过的渠道仍可被重新选择。
	target := chooseChannelAffinityFRTGlobalTarget(
		selectionScope,
		[]*model.Channel{{Id: fastChannelID}, {Id: currentChannelID}},
		currentChannelID,
		30_000,
		0,
		1,
		now,
	)
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
