package common

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamStatus_CaptureStructuredUpstreamError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, data, event string
		want              *UpstreamErrorDetails
	}{
		{"chat error", `{"error":{"code":"context_length_exceeded","type":"invalid_request_error","message":"Too many tokens","param":"messages"}}`, "", &UpstreamErrorDetails{Code: "context_length_exceeded", Type: "invalid_request_error", Message: "Too many tokens", Param: "messages"}},
		{"flat responses error", `{"type":"error","code":"rate_limit_exceeded","message":"Slow down","param":null}`, "", &UpstreamErrorDetails{Code: "rate_limit_exceeded", Message: "Slow down"}},
		{"failed response", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"Try again"}}}`, "", &UpstreamErrorDetails{Code: "server_error", Message: "Try again"}},
		{"done failed response", `{"type":"response.done","response":{"status":"failed","error":{"code":"new_provider_error"}}}`, "", &UpstreamErrorDetails{Code: "new_provider_error"}},
		{"completed failed response", `{"type":"response.completed","response":{"status":"failed","error":{"type":"provider_failure"}}}`, "", &UpstreamErrorDetails{Type: "provider_failure"}},
		{"named event", `{"code":"new_provider_error","message":"Unavailable","status_code":503}`, "error", &UpstreamErrorDetails{Code: "new_provider_error", Message: "Unavailable", StatusCode: 503}},
		{"named event with error type", `{"type":"rate_limit_error","code":"quota_exhausted"}`, "error", &UpstreamErrorDetails{Code: "quota_exhausted", Type: "rate_limit_error"}},
		{"payload identifies error", `{"type":"error","code":"server_error"}`, "message", &UpstreamErrorDetails{Code: "server_error"}},
		{"raw nested error type", `{"error":{"type":"upstream_error","message":"Unavailable"}}`, "", &UpstreamErrorDetails{Type: "upstream_error", Message: "Unavailable"}},
		{"numeric code and envelope status", `{"error":{"code":429,"message":"Slow down"},"status":429}`, "", &UpstreamErrorDetails{Code: "429", Message: "Slow down", StatusCode: 429}},
		{"code only", `{"type":"error","code":"server_is_overloaded"}`, "", &UpstreamErrorDetails{Code: "server_is_overloaded"}},
		{"string error", `{"error":"Unavailable"}`, "", &UpstreamErrorDetails{Message: "Unavailable"}},
		{"no fabricated status", `{"type":"error","code":"rate_limit_exceeded","status_code":200}`, "", &UpstreamErrorDetails{Code: "rate_limit_exceeded"}},
		{"no object fields", `{"error":{"code":{"secret":"hidden"},"message":["hidden"],"param":{"secret":"hidden"}}}`, "", nil},
		{"null error", `{"error":null}`, "", nil},
		{"successful response", `{"type":"response.completed","response":{"status":"completed","error":null}}`, "", nil},
		{"ordinary message", `{"type":"response.output_text.delta","message":"at capacity","delta":"hello"}`, "", nil},
		{"malformed payload", `{"error":{"message":"truncated"}`, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamStatus()
			s.CaptureUpstreamSSEError(tc.data, tc.event)
			assert.Equal(t, tc.want, s.UpstreamErrorDetails())
			if tc.want == nil {
				assert.Empty(t, s.UpstreamErrorMessage())
				return
			}
			assert.Equal(t, tc.want.Message, s.UpstreamErrorMessage())
			s.CaptureUpstreamError(`{"type":"error","message":"context canceled"}`)
			assert.Equal(t, tc.want, s.UpstreamErrorDetails(), "retain the first upstream cause, including code-only errors")
			snapshot := s.UpstreamErrorDetails()
			snapshot.Message = "changed outside the stream"
			assert.Equal(t, tc.want, s.UpstreamErrorDetails(), "return a detached snapshot")
		})
	}
}

func TestStreamStatus_UpstreamErrorFieldsAreMaskedAndBounded(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()
	s.CaptureUpstreamError(`{"error":{"code":"channel-secret","type":"api_key:secret","message":" api_key:secret\nretry\u0000later ","param":"`+strings.Repeat("x", 2100)+`"},"metadata":{"token":"private"}}`, "channel-secret")
	details := s.UpstreamErrorDetails()
	require.NotNil(t, details)
	assert.Equal(t, "***", details.Code)
	assert.Equal(t, "api_key:***", details.Type)
	assert.Equal(t, "api_key:*** retry later", details.Message)
	assert.Equal(t, strings.Repeat("x", 2048)+"…", details.Param)
}

func TestStreamStatus_SetEndReason_FirstWins(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.SetEndReason(StreamEndReasonDone, nil)
	s.SetEndReason(StreamEndReasonTimeout, nil)
	s.SetEndReason(StreamEndReasonClientGone, fmt.Errorf("context canceled"))

	assert.Equal(t, StreamEndReasonDone, s.EndReason)
	assert.Nil(t, s.EndError)
}

func TestStreamStatus_SetEndReason_WithError(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	expectedErr := fmt.Errorf("read: connection reset")
	s.SetEndReason(StreamEndReasonScannerErr, expectedErr)

	assert.Equal(t, StreamEndReasonScannerErr, s.EndReason)
	assert.Equal(t, expectedErr, s.EndError)
}

func TestStreamStatus_SetEndReason_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.SetEndReason(StreamEndReasonDone, nil)
}

func TestStreamStatus_SetEndReason_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	reasons := []StreamEndReason{
		StreamEndReasonDone,
		StreamEndReasonTimeout,
		StreamEndReasonClientGone,
		StreamEndReasonScannerErr,
		StreamEndReasonHandlerStop,
		StreamEndReasonEOF,
		StreamEndReasonPanic,
		StreamEndReasonPingFail,
	}

	var wg sync.WaitGroup
	for _, r := range reasons {
		wg.Add(1)
		go func(reason StreamEndReason) {
			defer wg.Done()
			s.SetEndReason(reason, nil)
		}(r)
	}
	wg.Wait()

	assert.NotEqual(t, StreamEndReasonNone, s.EndReason)
}

func TestStreamStatus_RecordError_Basic(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	s.RecordError("bad json")
	s.RecordError("another bad json")
	s.RecordError("client gone")

	assert.True(t, s.HasErrors())
	assert.Equal(t, 3, s.TotalErrorCount())
	assert.Len(t, s.Errors, 3)
}

func TestStreamStatus_RecordError_CapAtMax(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	for i := range 30 {
		s.RecordError(fmt.Sprintf("error_%d", i))
	}

	assert.Equal(t, maxStreamErrorEntries, len(s.Errors))
	assert.Equal(t, 30, s.TotalErrorCount())
}

func TestStreamStatus_RecordError_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	s.RecordError("should not panic")
}

func TestStreamStatus_RecordError_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s.RecordError(fmt.Sprintf("error_%d", idx))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 100, s.TotalErrorCount())
	assert.LessOrEqual(t, len(s.Errors), maxStreamErrorEntries)
}

func TestStreamStatus_HasErrors_Empty(t *testing.T) {
	t.Parallel()
	s := NewStreamStatus()
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_HasErrors_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.False(t, s.HasErrors())
	assert.Equal(t, 0, s.TotalErrorCount())
}

func TestStreamStatus_IsNormalEnd(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason StreamEndReason
		normal bool
	}{
		{StreamEndReasonDone, true},
		{StreamEndReasonEOF, true},
		{StreamEndReasonHandlerStop, true},
		{StreamEndReasonTimeout, false},
		{StreamEndReasonClientGone, false},
		{StreamEndReasonScannerErr, false},
		{StreamEndReasonPanic, false},
		{StreamEndReasonPingFail, false},
		{StreamEndReasonNone, false},
	}
	for _, tt := range tests {
		s := NewStreamStatus()
		s.SetEndReason(tt.reason, nil)
		assert.Equal(t, tt.normal, s.IsNormalEnd(), "reason=%s", tt.reason)
	}
}

func TestStreamStatus_IsNormalEnd_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.True(t, s.IsNormalEnd())
}

func TestStreamStatus_Summary(t *testing.T) {
	t.Parallel()

	s := NewStreamStatus()
	s.SetEndReason(StreamEndReasonDone, nil)
	summary := s.Summary()
	assert.Contains(t, summary, "reason=done")
	assert.NotContains(t, summary, "soft_errors")

	s2 := NewStreamStatus()
	s2.SetEndReason(StreamEndReasonTimeout, nil)
	s2.RecordError("bad json")
	s2.RecordError("write failed")
	summary2 := s2.Summary()
	assert.Contains(t, summary2, "reason=timeout")
	assert.Contains(t, summary2, "soft_errors=2")
}

func TestStreamStatus_Summary_NilSafe(t *testing.T) {
	t.Parallel()
	var s *StreamStatus
	assert.Equal(t, "StreamStatus<nil>", s.Summary())
}
