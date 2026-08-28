package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestPersistSuccessfulChannelAffinityMergesConcurrentFRTUpdate(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	cacheKey := fmt.Sprintf("test-success-write:%d", time.Now().UnixNano())
	cache := getChannelAffinityCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	initial := buildChannelAffinityStateForTest(43, time.Minute)
	require.NoError(t, cache.SetWithTTL(cacheKey, initial, time.Minute))

	// Simulate the FRT writer advancing the version while preserving the
	// original affinity channel and adding its real observation.
	latest := initial
	latest.FRT = &channelAffinityFRTState{Scopes: []channelAffinityFRTScopeState{{
		channelAffinityFRTScope: channelAffinityFRTScope{Group: "default", ModelName: "grok-4.6"},
		Channels: []channelAffinityFRTChannelScore{{
			ChannelID: 48,
			Samples:   []channelAffinityFRTSample{{FRTMs: 3600, ObservedAt: time.Now().UnixMilli()}},
		}},
	}}}
	latest.VersionEpoch, latest.VersionSeq = nextChannelAffinityVersion()
	require.NoError(t, cache.SetWithTTL(cacheKey, latest, time.Minute))

	updated, err := persistSuccessfulChannelAffinity(
		cacheKey,
		initial,
		48,
		time.Minute,
		&operation_setting.ChannelAffinitySetting{UpwardProbeIntervalSeconds: 3600},
	)
	require.NoError(t, err)
	require.True(t, updated)

	stored, found, err := cache.Get(cacheKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 48, stored.ChannelID)
	require.NotNil(t, stored.FRT)
	require.Len(t, stored.FRT.Scopes, 1)
	require.Equal(t, 3600.0, stored.FRT.Scopes[0].Channels[0].Samples[0].FRTMs)
}

func TestPersistSuccessfulChannelAffinityDoesNotOverwriteNewerRoutingDecision(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	cacheKey := fmt.Sprintf("test-success-write-newer:%d", time.Now().UnixNano())
	cache := getChannelAffinityCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	initial := buildChannelAffinityStateForTest(43, time.Minute)
	require.NoError(t, cache.SetWithTTL(cacheKey, initial, time.Minute))

	newer := initial
	newer.ChannelID = 99
	newer.VersionEpoch, newer.VersionSeq = nextChannelAffinityVersion()
	require.NoError(t, cache.SetWithTTL(cacheKey, newer, time.Minute))

	updated, err := persistSuccessfulChannelAffinity(
		cacheKey,
		initial,
		48,
		time.Minute,
		&operation_setting.ChannelAffinitySetting{UpwardProbeIntervalSeconds: 3600},
	)
	require.NoError(t, err)
	require.False(t, updated)

	stored, found, err := cache.Get(cacheKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 99, stored.ChannelID)
}

func TestPersistSuccessfulChannelAffinityRefreshPreservesFRTState(t *testing.T) {
	previousRedisEnabled := common.RedisEnabled
	previousRedisClient := common.RDB
	common.RedisEnabled = false
	common.RDB = nil
	t.Cleanup(func() {
		common.RedisEnabled = previousRedisEnabled
		common.RDB = previousRedisClient
	})

	cacheKey := fmt.Sprintf("test-success-write-refresh:%d", time.Now().UnixNano())
	cache := getChannelAffinityCache()
	t.Cleanup(func() { _, _ = cache.DeleteMany([]string{cacheKey}) })

	initial := buildChannelAffinityStateForTest(43, time.Minute)
	initial.FRT = &channelAffinityFRTState{Scopes: []channelAffinityFRTScopeState{{
		channelAffinityFRTScope: channelAffinityFRTScope{Group: "default", ModelName: "grok-4.6"},
		Channels: []channelAffinityFRTChannelScore{{
			ChannelID: 43,
			Samples:   []channelAffinityFRTSample{{FRTMs: 4200, ObservedAt: time.Now().UnixMilli()}},
		}},
	}}}
	require.NoError(t, cache.SetWithTTL(cacheKey, initial, time.Minute))

	updated, err := persistSuccessfulChannelAffinity(
		cacheKey,
		initial,
		43,
		time.Minute,
		&operation_setting.ChannelAffinitySetting{UpwardProbeIntervalSeconds: 3600},
	)
	require.NoError(t, err)
	require.True(t, updated)

	stored, found, err := cache.Get(cacheKey)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 43, stored.ChannelID)
	require.NotNil(t, stored.FRT)
	require.Equal(t, 4200.0, stored.FRT.Scopes[0].Channels[0].Samples[0].FRTMs)
}
