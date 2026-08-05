package common

import (
	"sync"
	"time"
)

// UpstreamAttemptTiming stores client-side timings for one upstream attempt.
// The timestamps are kept in memory and converted to durations when the log is
// generated, so incomplete attempts can be represented without inventing 0ms.
type UpstreamAttemptTiming struct {
	RetryIndex int
	ChannelID  int
	BodyBytes  int64

	RequestStart         time.Time
	GetConn              time.Time
	GotConn              time.Time
	ConnReused           bool
	ConnIdle             time.Duration
	DNSStart             time.Time
	DNSDone              time.Time
	ConnectStart         time.Time
	ConnectDone          time.Time
	TLSHandshakeStart    time.Time
	TLSHandshakeDone     time.Time
	WroteRequest         time.Time
	WriteErr             error
	FirstByte            time.Time
	ResponseHeader       time.Time
	FirstSSE             time.Time
	StreamEnd            time.Time
	DownstreamFirstEvent time.Time
	DownstreamEnd        time.Time
	StatusCode           int
	DoErr                error

	mu sync.Mutex
}

// Update applies a trace callback update atomically with log snapshotting.
func (t *UpstreamAttemptTiming) Update(fn func(*UpstreamAttemptTiming)) {
	if t == nil || fn == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(t)
}

type upstreamTimingState struct {
	mu       sync.Mutex
	attempts []*UpstreamAttemptTiming
}

func (info *RelayInfo) ensureUpstreamTimingState() *upstreamTimingState {
	if info == nil {
		return nil
	}
	if info.upstreamTimingState == nil {
		info.upstreamTimingState = &upstreamTimingState{}
	}
	return info.upstreamTimingState
}

// BeginUpstreamAttempt starts a timing record for the current channel attempt.
func (info *RelayInfo) BeginUpstreamAttempt(requestStart time.Time) *UpstreamAttemptTiming {
	state := info.ensureUpstreamTimingState()
	if state == nil {
		return nil
	}
	attempt := &UpstreamAttemptTiming{
		RetryIndex:   info.RetryIndex,
		ChannelID:    info.GetChannelID(),
		BodyBytes:    info.UpstreamRequestBodySize,
		RequestStart: requestStart,
	}
	state.mu.Lock()
	state.attempts = append(state.attempts, attempt)
	state.mu.Unlock()
	return attempt
}

func (info *RelayInfo) updateLatestUpstreamAttempt(fn func(*UpstreamAttemptTiming)) {
	state := info.ensureUpstreamTimingState()
	if state == nil || fn == nil {
		return
	}
	state.mu.Lock()
	if len(state.attempts) == 0 {
		state.mu.Unlock()
		return
	}
	attempt := state.attempts[len(state.attempts)-1]
	state.mu.Unlock()
	attempt.Update(fn)
}

// MarkUpstreamFirstSSE associates the first valid SSE event with the latest
// upstream attempt. It is intentionally independent from RelayInfo's existing
// FirstResponseTime, which is the aggregate FRT across retries.
func (info *RelayInfo) MarkUpstreamFirstSSE(at time.Time) {
	info.updateLatestUpstreamAttempt(func(attempt *UpstreamAttemptTiming) {
		if attempt.FirstSSE.IsZero() {
			attempt.FirstSSE = at
		}
	})
}

// MarkUpstreamStreamEnd records the end of the latest streamed response.
func (info *RelayInfo) MarkUpstreamStreamEnd(at time.Time) {
	info.updateLatestUpstreamAttempt(func(attempt *UpstreamAttemptTiming) {
		if attempt.StreamEnd.IsZero() {
			attempt.StreamEnd = at
		}
	})
}

// MarkDownstreamFirstEvent records when the first upstream event has completed
// local adaptation and downstream writing.
func (info *RelayInfo) MarkDownstreamFirstEvent(at time.Time) {
	info.updateLatestUpstreamAttempt(func(attempt *UpstreamAttemptTiming) {
		if attempt.DownstreamFirstEvent.IsZero() {
			attempt.DownstreamFirstEvent = at
		}
	})
}

// MarkDownstreamEnd records when the downstream event handler has drained all
// events. Comparing it with StreamEnd exposes local write backpressure.
func (info *RelayInfo) MarkDownstreamEnd(at time.Time) {
	info.updateLatestUpstreamAttempt(func(attempt *UpstreamAttemptTiming) {
		if attempt.DownstreamEnd.IsZero() {
			attempt.DownstreamEnd = at
		}
	})
}

// UpstreamTimingForLog returns compact, admin-only timing data for all
// upstream attempts. Missing phases are omitted instead of being reported as
// zero, which keeps failed and non-streaming attempts distinguishable.
func (info *RelayInfo) UpstreamTimingForLog() map[string]interface{} {
	state := info.ensureUpstreamTimingState()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	attempts := append([]*UpstreamAttemptTiming(nil), state.attempts...)
	state.mu.Unlock()
	if len(attempts) == 0 {
		return nil
	}

	loggedAttempts := make([]map[string]interface{}, 0, len(attempts))
	for _, attempt := range attempts {
		loggedAttempts = append(loggedAttempts, attempt.logMap())
	}
	return map[string]interface{}{"attempts": loggedAttempts}
}

func (attempt *UpstreamAttemptTiming) logMap() map[string]interface{} {
	attempt.mu.Lock()
	retryIndex := attempt.RetryIndex
	channelID := attempt.ChannelID
	bodyBytes := attempt.BodyBytes
	requestStart := attempt.RequestStart
	gotConn := attempt.GotConn
	connReused := attempt.ConnReused
	connIdle := attempt.ConnIdle
	dnsStart := attempt.DNSStart
	dnsDone := attempt.DNSDone
	connectStart := attempt.ConnectStart
	connectDone := attempt.ConnectDone
	tlsHandshakeStart := attempt.TLSHandshakeStart
	tlsHandshakeDone := attempt.TLSHandshakeDone
	wroteRequest := attempt.WroteRequest
	writeErr := attempt.WriteErr
	firstByte := attempt.FirstByte
	responseHeader := attempt.ResponseHeader
	firstSSE := attempt.FirstSSE
	streamEnd := attempt.StreamEnd
	downstreamFirstEvent := attempt.DownstreamFirstEvent
	downstreamEnd := attempt.DownstreamEnd
	statusCode := attempt.StatusCode
	doErr := attempt.DoErr
	attempt.mu.Unlock()

	result := map[string]interface{}{
		"retry_index": retryIndex,
		"channel_id":  channelID,
	}
	if bodyBytes > 0 {
		result["body_bytes"] = bodyBytes
	}
	if !gotConn.IsZero() {
		result["conn_reused"] = connReused
		if connReused && connIdle > 0 {
			result["conn_idle_ms"] = connIdle.Milliseconds()
		}
	}
	setDuration(result, "conn_acquire_ms", requestStart, gotConn)
	setDuration(result, "dns_ms", dnsStart, dnsDone)
	setDuration(result, "dial_ms", connectStart, connectDone)
	setDuration(result, "tls_handshake_ms", tlsHandshakeStart, tlsHandshakeDone)
	setDuration(result, "upload_ms", gotConn, wroteRequest)
	setDuration(result, "response_header_wait_ms", wroteRequest, responseHeader)
	setDuration(result, "first_byte_ms", requestStart, firstByte)
	setDuration(result, "header_to_first_sse_ms", responseHeader, firstSSE)
	setDuration(result, "upstream_first_sse_ms", requestStart, firstSSE)
	setDuration(result, "stream_end_ms", requestStart, streamEnd)
	setDuration(result, "downstream_first_event_ms", requestStart, downstreamFirstEvent)
	setDuration(result, "downstream_end_ms", requestStart, downstreamEnd)
	setDuration(result, "upstream_to_downstream_first_ms", firstSSE, downstreamFirstEvent)
	setDuration(result, "upstream_to_downstream_end_ms", streamEnd, downstreamEnd)
	if statusCode != 0 {
		result["status_code"] = statusCode
	}
	if writeErr != nil {
		result["write_error"] = writeErr.Error()
	}
	if doErr != nil {
		result["error"] = doErr.Error()
	}
	return result
}

func setDuration(result map[string]interface{}, key string, start, end time.Time) {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return
	}
	result[key] = end.Sub(start).Milliseconds()
}
