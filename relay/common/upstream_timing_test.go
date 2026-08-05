package common

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamTimingForLogKeepsRetryAttemptsAndPhaseDurations(t *testing.T) {
	start := time.Unix(100, 0)
	info := &RelayInfo{
		RetryIndex:              0,
		UpstreamRequestBodySize: 1024,
		ChannelMeta:             &ChannelMeta{ChannelId: 9},
	}
	first := info.BeginUpstreamAttempt(start)
	first.Update(func(attempt *UpstreamAttemptTiming) {
		attempt.GotConn = start.Add(10 * time.Millisecond)
		attempt.WroteRequest = start.Add(30 * time.Millisecond)
		attempt.ResponseHeader = start.Add(80 * time.Millisecond)
		attempt.FirstSSE = start.Add(120 * time.Millisecond)
		attempt.StreamEnd = start.Add(220 * time.Millisecond)
		attempt.ConnReused = true
		attempt.ConnIdle = 1500 * time.Millisecond
		attempt.StatusCode = http.StatusOK
	})

	info.RetryIndex = 1
	info.ChannelMeta.ChannelId = 12
	second := info.BeginUpstreamAttempt(start.Add(300 * time.Millisecond))
	second.Update(func(attempt *UpstreamAttemptTiming) {
		attempt.StatusCode = http.StatusUnauthorized
		attempt.DoErr = assert.AnError
	})

	logged := info.UpstreamTimingForLog()
	attempts, ok := logged["attempts"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, attempts, 2)
	assert.Equal(t, 0, attempts[0]["retry_index"])
	assert.Equal(t, 9, attempts[0]["channel_id"])
	assert.Equal(t, int64(20), attempts[0]["upload_ms"])
	assert.Equal(t, int64(50), attempts[0]["response_header_wait_ms"])
	assert.Equal(t, int64(40), attempts[0]["header_to_first_sse_ms"])
	assert.Equal(t, int64(120), attempts[0]["upstream_first_sse_ms"])
	assert.Equal(t, int64(220), attempts[0]["stream_end_ms"])
	assert.Equal(t, int64(1500), attempts[0]["conn_idle_ms"])
	assert.Equal(t, 401, attempts[1]["status_code"])
	assert.Equal(t, assert.AnError.Error(), attempts[1]["error"])
}

func TestUpstreamTimingForLogCanBeUpdatedByStreamScanner(t *testing.T) {
	start := time.Unix(200, 0)
	info := &RelayInfo{ChannelMeta: &ChannelMeta{ChannelId: 42}}
	info.BeginUpstreamAttempt(start)
	firstSSE := start.Add(2 * time.Second)
	streamEnd := start.Add(3 * time.Second)

	info.MarkUpstreamFirstSSE(firstSSE)
	info.MarkUpstreamFirstSSE(firstSSE.Add(time.Second))
	info.MarkUpstreamStreamEnd(streamEnd)
	info.MarkUpstreamStreamEnd(streamEnd.Add(time.Second))

	logged := info.UpstreamTimingForLog()
	attempts := logged["attempts"].([]map[string]interface{})
	require.Len(t, attempts, 1)
	assert.Equal(t, int64(2000), attempts[0]["upstream_first_sse_ms"])
	assert.Equal(t, int64(3000), attempts[0]["stream_end_ms"])
}
