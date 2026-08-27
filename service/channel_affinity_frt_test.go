package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/common"
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
	setting := &operation_setting.ChannelAffinitySetting{FRTProbeCount: 2}
	scope := &channelAffinityFRTScopeState{ConsecutiveSlow: 1}
	assert.False(t, channelAffinityFRTShouldEvaluate(scope, 12_000, setting))

	scope.ConsecutiveSlow = 2
	assert.True(t, channelAffinityFRTShouldEvaluate(scope, 12_000, setting))

	scope.ConsecutiveSlow = 0
	assert.True(t, channelAffinityFRTShouldEvaluate(scope, int64(channelAffinityFRTExplosionMs), setting))
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

func TestChannelAffinityFRTObservationScopeKeepsStreamSeparate(t *testing.T) {
	meta := channelAffinityMeta{ModelName: "gpt-5", RequestPath: "/v1/responses"}
	selection := channelAffinitySelection{Group: "default"}
	scope := channelAffinityFRTScopeForObservation(meta, selection, &common.RelayInfo{IsStream: true})
	assert.True(t, scope.Stream)
	assert.Equal(t, "default", scope.Group)
}
