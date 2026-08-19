package service

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/google/uuid"
	"github.com/samber/hot"
)

const (
	channelAffinityCacheNamespace     = "new-api:channel_affinity:v3"
	defaultUpwardProbeIntervalSeconds = 3600
	channelAffinityMemoryLockShards   = 256
	channelAffinityRedisTimeout       = 2 * time.Second
)

var (
	channelAffinityCacheOnce sync.Once
	channelAffinityCache     *cachex.HybridCache[channelAffinityState]

	channelAffinityStateLocks      [channelAffinityMemoryLockShards]sync.Mutex
	channelAffinityVersionSequence atomic.Uint64
	channelAffinityVersionEpoch    = newChannelAffinityVersionEpoch()
)

type channelAffinityState struct {
	ChannelID     int
	VersionEpoch  uint64
	VersionSeq    uint64
	NextProbeAt   int64
	IdleExpiresAt int64
	FRT           *channelAffinityFRTState
}

const maxChannelAffinityFRTChannels = 16

type channelAffinityFRTChannelScore struct {
	ChannelID      int       `json:"channel_id"`
	BaselineFRTMs  float64   `json:"baseline_frt_ms"`
	PeakScoreMs    float64   `json:"peak_score_ms"`
	LastObservedAt int64     `json:"last_observed_at"`
	RecentFRTMs    []float64 `json:"recent_frt_ms,omitempty"`
}

type channelAffinityFRTState struct {
	Group                string                           `json:"group"`
	Priority             int64                            `json:"priority"`
	ConsecutiveSlow      int                              `json:"consecutive_slow"`
	VisitedChannelIDs    []int                            `json:"visited_channel_ids,omitempty"`
	AllSlowHoldChannelID int                              `json:"all_slow_hold_channel_id,omitempty"`
	AllSlowHoldUntil     int64                            `json:"all_slow_hold_until,omitempty"`
	Channels             []channelAffinityFRTChannelScore `json:"channels,omitempty"`
}

type channelAffinityRequestState struct {
	State channelAffinityState
	Found bool
}

type channelAffinityStateCodec struct{}

func (channelAffinityStateCodec) Encode(state channelAffinityState) (string, error) {
	if state.ChannelID <= 0 || state.VersionEpoch == 0 || state.VersionSeq == 0 ||
		state.NextProbeAt <= 0 || state.IdleExpiresAt <= 0 {
		return "", fmt.Errorf("invalid channel affinity state")
	}
	encoded := fmt.Sprintf(
		"%d:%d:%d:%d:%d",
		state.ChannelID,
		state.VersionEpoch,
		state.VersionSeq,
		state.NextProbeAt,
		state.IdleExpiresAt,
	)
	if state.FRT == nil {
		return encoded, nil
	}
	payload, err := common.Marshal(state.FRT)
	if err != nil {
		return "", fmt.Errorf("encode channel affinity frt state: %w", err)
	}
	return encoded + ":" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (channelAffinityStateCodec) Decode(value string) (channelAffinityState, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 6)
	if len(parts) < 5 {
		return channelAffinityState{}, fmt.Errorf("invalid channel affinity state encoding")
	}

	channelID, err := strconv.Atoi(parts[0])
	if err != nil {
		return channelAffinityState{}, fmt.Errorf("decode channel affinity channel id: %w", err)
	}
	versionEpoch, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return channelAffinityState{}, fmt.Errorf("decode channel affinity version epoch: %w", err)
	}
	versionSeq, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return channelAffinityState{}, fmt.Errorf("decode channel affinity version sequence: %w", err)
	}
	nextProbeAt, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return channelAffinityState{}, fmt.Errorf("decode channel affinity next probe time: %w", err)
	}
	idleExpiresAt, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return channelAffinityState{}, fmt.Errorf("decode channel affinity idle expiry: %w", err)
	}

	state := channelAffinityState{
		ChannelID:     channelID,
		VersionEpoch:  versionEpoch,
		VersionSeq:    versionSeq,
		NextProbeAt:   nextProbeAt,
		IdleExpiresAt: idleExpiresAt,
	}
	if len(parts) == 6 && parts[5] != "" {
		payload, err := base64.RawURLEncoding.DecodeString(parts[5])
		if err != nil {
			return channelAffinityState{}, fmt.Errorf("decode channel affinity frt payload: %w", err)
		}
		var frt channelAffinityFRTState
		if err := common.Unmarshal(payload, &frt); err != nil {
			return channelAffinityState{}, fmt.Errorf("decode channel affinity frt state: %w", err)
		}
		state.FRT = &frt
	}
	if _, err := (channelAffinityStateCodec{}).Encode(state); err != nil {
		return channelAffinityState{}, err
	}
	return state, nil
}

func getChannelAffinityCache() *cachex.HybridCache[channelAffinityState] {
	channelAffinityCacheOnce.Do(func() {
		setting := operation_setting.GetChannelAffinitySetting()
		capacity := setting.MaxEntries
		if capacity <= 0 {
			capacity = 100_000
		}
		defaultTTLSeconds := setting.DefaultTTLSeconds
		if defaultTTLSeconds <= 0 {
			defaultTTLSeconds = 3600
		}

		channelAffinityCache = cachex.NewHybridCache[channelAffinityState](cachex.HybridCacheConfig[channelAffinityState]{
			Namespace: cachex.Namespace(channelAffinityCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: channelAffinityStateCodec{},
			Memory: func() *hot.HotCache[string, channelAffinityState] {
				return hot.NewHotCache[string, channelAffinityState](hot.LRU, capacity).
					WithTTL(time.Duration(defaultTTLSeconds) * time.Second).
					WithJanitor().
					Build()
			},
		})
	})
	return channelAffinityCache
}

func newChannelAffinityVersionEpoch() uint64 {
	id := uuid.New()
	epoch := binary.BigEndian.Uint64(id[:8])
	if epoch == 0 {
		return 1
	}
	return epoch
}

func nextChannelAffinityVersion() (uint64, uint64) {
	sequence := channelAffinityVersionSequence.Add(1)
	if sequence == 0 {
		panic("channel affinity version sequence exhausted")
	}
	return channelAffinityVersionEpoch, sequence
}

func channelAffinityStateLock(cacheKey string) *sync.Mutex {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(cacheKey))
	return &channelAffinityStateLocks[hash.Sum32()%channelAffinityMemoryLockShards]
}

func channelAffinityProbeInterval(setting *operation_setting.ChannelAffinitySetting) time.Duration {
	seconds := defaultUpwardProbeIntervalSeconds
	if setting != nil && setting.UpwardProbeIntervalSeconds > 0 {
		seconds = setting.UpwardProbeIntervalSeconds
	}
	return time.Duration(seconds) * time.Second
}

func sameChannelAffinityVersion(left, right channelAffinityState) bool {
	return left.VersionEpoch == right.VersionEpoch && left.VersionSeq == right.VersionSeq
}

const channelAffinityRefreshScript = `
local current = redis.call('GET', KEYS[1])
if not current then return 0 end
local channel, epoch, sequence, next_probe, idle_expires = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)$')
local suffix = ''
if not channel then
  channel, epoch, sequence, next_probe, idle_expires, suffix = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)(:.*)$')
end
if not channel or epoch ~= ARGV[1] or sequence ~= ARGV[2] then return 0 end
local ttl_ms = tonumber(ARGV[4])
if not ttl_ms or ttl_ms <= 0 then return 0 end
local updated = channel .. ':' .. epoch .. ':' .. sequence .. ':' .. next_probe .. ':' .. ARGV[3] .. (suffix or '')
redis.call('PSETEX', KEYS[1], ttl_ms, updated)
return 1
`

const channelAffinityClaimProbeScript = `
local current = redis.call('GET', KEYS[1])
if not current then return 0 end
local channel, epoch, sequence, next_probe, idle_expires = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)$')
local suffix = ''
if not channel then
  channel, epoch, sequence, next_probe, idle_expires, suffix = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)(:.*)$')
end
if not channel or epoch ~= ARGV[1] or sequence ~= ARGV[2] then return 0 end
local now_ms = tonumber(ARGV[3])
if not now_ms or tonumber(next_probe) > now_ms then return 0 end
local ttl_ms = tonumber(idle_expires) - now_ms
if ttl_ms <= 0 then return 0 end
local updated = channel .. ':' .. epoch .. ':' .. sequence .. ':' .. ARGV[4] .. ':' .. idle_expires .. (suffix or '')
redis.call('PSETEX', KEYS[1], ttl_ms, updated)
return 1
`

const channelAffinitySwitchScript = `
local current = redis.call('GET', KEYS[1])
if not current then return 0 end
local _, epoch, sequence = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)$')
if not epoch then
  _, epoch, sequence = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)(:.*)$')
end
if not epoch or epoch ~= ARGV[1] or sequence ~= ARGV[2] then return 0 end
local ttl_ms = tonumber(ARGV[4])
if not ttl_ms or ttl_ms <= 0 then return 0 end
redis.call('PSETEX', KEYS[1], ttl_ms, ARGV[3])
return 1
`

const channelAffinityDeleteScript = `
local current = redis.call('GET', KEYS[1])
if not current then return 0 end
local _, epoch, sequence = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)$')
if not epoch then
  _, epoch, sequence = string.match(current, '^(-?%d+):(%d+):(%d+):(-?%d+):(-?%d+)(:.*)$')
end
if not epoch or epoch ~= ARGV[1] or sequence ~= ARGV[2] then return 0 end
return redis.call('UNLINK', KEYS[1])
`

func createChannelAffinityState(cacheKey string, state channelAffinityState, ttl time.Duration) (bool, error) {
	cache := getChannelAffinityCache()
	if common.RedisEnabled && common.RDB != nil {
		encoded, err := (channelAffinityStateCodec{}).Encode(state)
		if err != nil {
			return false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), channelAffinityRedisTimeout)
		defer cancel()
		return common.RDB.SetNX(ctx, cache.FullKey(cacheKey), encoded, ttl).Result()
	}

	lock := channelAffinityStateLock(cacheKey)
	lock.Lock()
	defer lock.Unlock()
	if _, found, err := cache.Get(cacheKey); err != nil || found {
		return false, err
	}
	if err := cache.SetWithTTL(cacheKey, state, ttl); err != nil {
		return false, err
	}
	return true, nil
}

func refreshChannelAffinityState(cacheKey string, expected channelAffinityState, idleExpiresAt int64, ttl time.Duration) (bool, error) {
	cache := getChannelAffinityCache()
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), channelAffinityRedisTimeout)
		defer cancel()
		result, err := common.RDB.Eval(
			ctx,
			channelAffinityRefreshScript,
			[]string{cache.FullKey(cacheKey)},
			strconv.FormatUint(expected.VersionEpoch, 10),
			strconv.FormatUint(expected.VersionSeq, 10),
			strconv.FormatInt(idleExpiresAt, 10),
			strconv.FormatInt(ttl.Milliseconds(), 10),
		).Int64()
		return result == 1, err
	}

	lock := channelAffinityStateLock(cacheKey)
	lock.Lock()
	defer lock.Unlock()
	current, found, err := cache.Get(cacheKey)
	if err != nil || !found || !sameChannelAffinityVersion(current, expected) {
		return false, err
	}
	current.IdleExpiresAt = idleExpiresAt
	if err := cache.SetWithTTL(cacheKey, current, ttl); err != nil {
		return false, err
	}
	return true, nil
}

func claimChannelAffinityProbe(cacheKey string, expected channelAffinityState, now, nextProbeAt int64) (bool, error) {
	cache := getChannelAffinityCache()
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), channelAffinityRedisTimeout)
		defer cancel()
		result, err := common.RDB.Eval(
			ctx,
			channelAffinityClaimProbeScript,
			[]string{cache.FullKey(cacheKey)},
			strconv.FormatUint(expected.VersionEpoch, 10),
			strconv.FormatUint(expected.VersionSeq, 10),
			strconv.FormatInt(now, 10),
			strconv.FormatInt(nextProbeAt, 10),
		).Int64()
		return result == 1, err
	}

	lock := channelAffinityStateLock(cacheKey)
	lock.Lock()
	defer lock.Unlock()
	current, found, err := cache.Get(cacheKey)
	if err != nil || !found || !sameChannelAffinityVersion(current, expected) {
		return false, err
	}
	if current.NextProbeAt > now || current.IdleExpiresAt <= now {
		return false, nil
	}
	current.NextProbeAt = nextProbeAt
	ttl := time.Duration(current.IdleExpiresAt-now) * time.Millisecond
	if err := cache.SetWithTTL(cacheKey, current, ttl); err != nil {
		return false, err
	}
	return true, nil
}

func switchChannelAffinityState(cacheKey string, expected, replacement channelAffinityState, ttl time.Duration) (bool, error) {
	cache := getChannelAffinityCache()
	if common.RedisEnabled && common.RDB != nil {
		encoded, err := (channelAffinityStateCodec{}).Encode(replacement)
		if err != nil {
			return false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), channelAffinityRedisTimeout)
		defer cancel()
		result, err := common.RDB.Eval(
			ctx,
			channelAffinitySwitchScript,
			[]string{cache.FullKey(cacheKey)},
			strconv.FormatUint(expected.VersionEpoch, 10),
			strconv.FormatUint(expected.VersionSeq, 10),
			encoded,
			strconv.FormatInt(ttl.Milliseconds(), 10),
		).Int64()
		return result == 1, err
	}

	lock := channelAffinityStateLock(cacheKey)
	lock.Lock()
	defer lock.Unlock()
	current, found, err := cache.Get(cacheKey)
	if err != nil || !found || !sameChannelAffinityVersion(current, expected) {
		return false, err
	}
	if err := cache.SetWithTTL(cacheKey, replacement, ttl); err != nil {
		return false, err
	}
	return true, nil
}

func deleteChannelAffinityState(cacheKey string, expected channelAffinityState) (bool, error) {
	cache := getChannelAffinityCache()
	if common.RedisEnabled && common.RDB != nil {
		ctx, cancel := context.WithTimeout(context.Background(), channelAffinityRedisTimeout)
		defer cancel()
		result, err := common.RDB.Eval(
			ctx,
			channelAffinityDeleteScript,
			[]string{cache.FullKey(cacheKey)},
			strconv.FormatUint(expected.VersionEpoch, 10),
			strconv.FormatUint(expected.VersionSeq, 10),
		).Int64()
		return result == 1, err
	}

	lock := channelAffinityStateLock(cacheKey)
	lock.Lock()
	defer lock.Unlock()
	current, found, err := cache.Get(cacheKey)
	if err != nil || !found || !sameChannelAffinityVersion(current, expected) {
		return false, err
	}
	deleted, err := cache.DeleteMany([]string{cacheKey})
	if err != nil {
		return false, err
	}
	return deleted[cache.FullKey(cacheKey)], nil
}
