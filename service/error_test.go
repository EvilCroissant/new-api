package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResetStatusCode(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		statusCode       int
		statusCodeConfig string
		expectedCode     int
	}{
		{
			name:             "map string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"503"}`,
			expectedCode:     503,
		},
		{
			name:             "map int value",
			statusCode:       429,
			statusCodeConfig: `{"429":503}`,
			expectedCode:     503,
		},
		{
			name:             "skip invalid string value",
			statusCode:       429,
			statusCodeConfig: `{"429":"bad-code"}`,
			expectedCode:     429,
		},
		{
			name:             "skip status code 200",
			statusCode:       200,
			statusCodeConfig: `{"200":503}`,
			expectedCode:     200,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			newAPIError := &types.NewAPIError{
				StatusCode: tc.statusCode,
			}
			ResetStatusCode(newAPIError, tc.statusCodeConfig)
			require.Equal(t, tc.expectedCode, newAPIError.StatusCode)
		})
	}
}

func TestRelayErrorHandlerTruncatesInvalidJSONBodyInLog(t *testing.T) {
	withDebugEnabled(t, false)

	body := strings.Repeat("b", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, "bad response status code 500", newAPIError.Error())
	require.Contains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), fmt.Sprintf("original_length=%d", len(body)))
	require.NotContains(t, logBuffer.String(), strings.Repeat("b", common.LocalLogContentLimit+1))
}

func TestRelayErrorHandlerKeepsStructuredErrorMessage(t *testing.T) {
	message := strings.Repeat("c", common.LocalLogContentLimit+256)
	body := `{"message":"` + message + `"}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerKeepsOpenAIErrorMessage(t *testing.T) {
	message := strings.Repeat("d", common.LocalLogContentLimit+256)
	body := `{"error":{"message":"` + message + `","type":"server_error","code":"server_error"}}`
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.Equal(t, message, newAPIError.Error())
}

func TestRelayErrorHandlerUpstreamErrorDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		want      *relaycommon.UpstreamErrorDetails
		wantError string
	}{
		{
			name: "JSON 504", status: 504,
			body:      `{"error":{"message":"Upstream service temporarily unavailable","type":"upstream_error"}}`,
			want:      &relaycommon.UpstreamErrorDetails{Type: "upstream_error", Message: "Upstream service temporarily unavailable", StatusCode: 504},
			wantError: "Upstream service temporarily unavailable",
		},
		{
			name: "JSON 503", status: 503,
			body:      `{"error":{"message":"Service temporarily unavailable","type":"api_error"}}`,
			want:      &relaycommon.UpstreamErrorDetails{Type: "api_error", Message: "Service temporarily unavailable", StatusCode: 503},
			wantError: "Service temporarily unavailable",
		},
		{
			name: "JSON 502", status: 502,
			body:      `{"error":{"message":"Upstream service temporarily unavailable","type":"upstream_error"}}`,
			want:      &relaycommon.UpstreamErrorDetails{Type: "upstream_error", Message: "Upstream service temporarily unavailable", StatusCode: 502},
			wantError: "Upstream service temporarily unavailable",
		},
		{
			name: "SSE 502", status: 502,
			body:      "event: error\ndata: {\"code\":\"upstream_stream_read_error\",\"message\":\"Upstream response stream was interrupted\",\"param\":null,\"sequence_number\":0,\"type\":\"error\"}\n\n",
			want:      &relaycommon.UpstreamErrorDetails{Code: "upstream_stream_read_error", Message: "Upstream response stream was interrupted", StatusCode: 502},
			wantError: "bad response status code 502",
		},
		{
			name: "SSE multiline and CRLF", status: 503,
			body:      "data: {\"type\":\"response.output_text.delta\",\"message\":\"ordinary output\"}\r\n\r\n: keepalive\r\nevent: error\r\ndata: {\r\ndata: \"error\":{\"type\":\"api_error\",\"message\":\"Unavailable\"}\r\ndata: }\r\n\r\n",
			want:      &relaycommon.UpstreamErrorDetails{Type: "api_error", Message: "Unavailable", StatusCode: 503},
			wantError: "bad response status code 503",
		},
		{
			name: "SSE named error without trailing separator", status: 502,
			body:      "event:error\rdata:{\"code\":429,\"message\":\"Busy\",\"status_code\":429}",
			want:      &relaycommon.UpstreamErrorDetails{Code: "429", Message: "Busy", StatusCode: 502},
			wantError: "bad response status code 502",
		},
		{
			name: "SSE response failed keeps first cause", status: 504,
			body:      "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\",\"message\":\"Unavailable\"}}}\n\nevent: error\ndata: {\"message\":\"context canceled\"}\n\n",
			want:      &relaycommon.UpstreamErrorDetails{Code: "server_error", Message: "Unavailable", StatusCode: 504},
			wantError: "bad response status code 504",
		},
		{
			name: "SSE code only", status: 502,
			body:      "data: {\"type\":\"error\",\"code\":\"future_provider_error\"}\n\n",
			want:      &relaycommon.UpstreamErrorDetails{Code: "future_provider_error", StatusCode: 502},
			wantError: "bad response status code 502",
		},
		{
			name: "SSE masks diagnostics", status: 502,
			body:      "event: error\ndata: {\"message\":\"api_key:secret\",\"param\":\"" + strings.Repeat("p", 2100) + "\"}\n\n",
			want:      &relaycommon.UpstreamErrorDetails{Message: "api_key:***", Param: strings.Repeat("p", 2048) + "…", StatusCode: 502},
			wantError: "bad response status code 502",
		},
		{
			name: "malformed SSE", status: 502,
			body:      "event: error\ndata: {\"message\":\"cut off\"\n\n",
			wantError: "bad response status code 502",
		},
		{
			name: "ordinary SSE messages are not errors", status: 502,
			body:      "event: message\ndata: {\"message\":\"ordinary output\"}\n\ndata: [DONE]\n\n",
			wantError: "bad response status code 502",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}
			apiError := RelayErrorHandler(context.Background(), resp, false)
			require.NotNil(t, apiError)
			assert.Equal(t, tc.status, apiError.StatusCode)
			assert.EqualError(t, apiError, tc.wantError)
			// Recording SSE diagnostics must not change retry/disable inputs.
			if strings.Contains(tc.name, "SSE") {
				assert.Equal(t, types.ErrorCodeBadResponseStatusCode, apiError.GetErrorCode())
			}
			ResetStatusCode(apiError, fmt.Sprintf(`{"%d":418}`, tc.status))
			var upstreamError *upstreamResponseError
			if tc.want == nil {
				assert.False(t, errors.As(apiError.Err, &upstreamError))
				return
			}
			require.True(t, errors.As(apiError.Err, &upstreamError))
			assert.Equal(t, tc.want, upstreamError.details, "retain the original upstream status after mapping")
		})
	}
}

func TestStreamErrorLogDiagnostics(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name       string
		end        relaycommon.StreamEndReason
		err        error
		terminal   bool
		requestCtx context.Context
		mark       func(*relaycommon.StreamStatus)
		wantStatus string
		want       *relaycommon.UpstreamErrorDetails
	}{
		{
			name: "missing terminal", end: relaycommon.StreamEndReasonEOF, terminal: true, wantStatus: "error",
			want: &relaycommon.UpstreamErrorDetails{Code: "upstream_stream_truncated", Type: "upstream_error", Message: "Upstream response stream ended before completion"},
		},
		{
			name: "read interrupted", end: relaycommon.StreamEndReasonScannerErr, err: io.ErrUnexpectedEOF, terminal: true, wantStatus: "error",
			want: &relaycommon.UpstreamErrorDetails{Code: "upstream_stream_read_error", Type: "upstream_error", Message: "Upstream response stream was interrupted"},
		},
		{
			name: "HTTP2 reset", end: relaycommon.StreamEndReasonScannerErr, err: errors.New("stream error: stream ID 3; INTERNAL_ERROR; received from peer"), terminal: true, wantStatus: "error",
			want: &relaycommon.UpstreamErrorDetails{Code: "upstream_http2_stream_error", Type: "upstream_error", Message: "Upstream HTTP/2 stream failed"},
		},
		{
			name: "upstream error survives client disconnect", end: relaycommon.StreamEndReasonClientGone, err: context.Canceled, terminal: true, wantStatus: "error",
			requestCtx: canceled,
			mark: func(s *relaycommon.StreamStatus) {
				s.CaptureUpstreamError(`{"error":{"code":"server_error","message":"Unavailable"}}`)
			},
			want: &relaycommon.UpstreamErrorDetails{Code: "server_error", Message: "Unavailable"},
		},
		{
			name: "code only error survives reader failure", end: relaycommon.StreamEndReasonScannerErr, err: io.ErrUnexpectedEOF, terminal: true, wantStatus: "error",
			mark: func(s *relaycommon.StreamStatus) {
				s.CaptureUpstreamError(`{"type":"error","code":"provider_failure"}`)
			},
			want: &relaycommon.UpstreamErrorDetails{Code: "provider_failure"},
		},
		{name: "legacy EOF without terminal contract", end: relaycommon.StreamEndReasonEOF, wantStatus: "ok"},
		{name: "done marker", end: relaycommon.StreamEndReasonDone, terminal: true, wantStatus: "ok"},
		{name: "completed response", end: relaycommon.StreamEndReasonEOF, terminal: true, mark: (*relaycommon.StreamStatus).MarkCompleted, wantStatus: "ok"},
		{
			name: "output token limit", end: relaycommon.StreamEndReasonEOF, terminal: true, wantStatus: "ok",
			mark: func(s *relaycommon.StreamStatus) { s.MarkIncomplete("max_output_tokens") },
		},
		{name: "protocol cancellation", end: relaycommon.StreamEndReasonEOF, terminal: true, mark: (*relaycommon.StreamStatus).MarkCancelled, wantStatus: "ok"},
		{name: "client disconnect", end: relaycommon.StreamEndReasonClientGone, err: context.Canceled, terminal: true, wantStatus: "error"},
		{name: "canceled reader", end: relaycommon.StreamEndReasonScannerErr, err: fmt.Errorf("read: %w", context.Canceled), terminal: true, wantStatus: "error"},
		{name: "canceled request with reader failure", end: relaycommon.StreamEndReasonScannerErr, err: io.ErrUnexpectedEOF, terminal: true, requestCtx: canceled, wantStatus: "error"},
		{name: "canceled request without terminal", end: relaycommon.StreamEndReasonEOF, terminal: true, requestCtx: canceled, wantStatus: "error"},
		{name: "deadline", end: relaycommon.StreamEndReasonScannerErr, err: context.DeadlineExceeded, terminal: true, wantStatus: "error"},
		{name: "scanner size limit", end: relaycommon.StreamEndReasonScannerErr, err: bufio.ErrTooLong, terminal: true, wantStatus: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := relaycommon.NewStreamStatus()
			if tc.terminal {
				stream.RequireTerminal()
			}
			if tc.mark != nil {
				tc.mark(stream)
			}
			stream.SetEndReason(tc.end, tc.err)
			before, upstreamBefore := stream.OutcomeSnapshot(), stream.UpstreamErrorDetails()
			other := model.NewLogOther()
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			if tc.requestCtx != nil {
				ctx.Request = ctx.Request.WithContext(tc.requestCtx)
			}
			appendStreamStatus(ctx, &relaycommon.RelayInfo{IsStream: true, StreamStatus: stream}, other)
			logged, ok := other.Snapshot()["stream_status"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.wantStatus, logged["status"])
			assert.Equal(t, string(tc.end), logged["end_reason"])
			if tc.err != nil {
				assert.Equal(t, tc.err.Error(), logged["end_error"])
			}
			if tc.want == nil {
				assert.NotContains(t, logged, "upstream_error_details")
				assert.NotContains(t, logged, "upstream_error")
			} else {
				assert.Equal(t, tc.want, logged["upstream_error_details"])
				if tc.want.Message != "" {
					assert.Equal(t, tc.want.Message, logged["upstream_error"])
				} else {
					assert.NotContains(t, logged, "upstream_error")
				}
			}
			assert.Equal(t, before, stream.OutcomeSnapshot(), "logging must not change relay classification")
			assert.Equal(t, upstreamBefore, stream.UpstreamErrorDetails(), "generated diagnostics stay in the log")
		})
	}
}

func TestRelayErrorHandlerKeepsInvalidJSONBodyInDebugLog(t *testing.T) {
	withDebugEnabled(t, true)

	body := strings.Repeat("e", common.LocalLogContentLimit+256)
	var logBuffer bytes.Buffer

	common.LogWriterMu.Lock()
	oldWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &logBuffer
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = oldWriter
		common.LogWriterMu.Unlock()
	})

	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	newAPIError := RelayErrorHandler(context.Background(), resp, false)

	require.NotNil(t, newAPIError)
	require.NotContains(t, logBuffer.String(), "[truncated")
	require.Contains(t, logBuffer.String(), body)
}

func withDebugEnabled(t *testing.T, enabled bool) {
	t.Helper()

	oldDebug := common.DebugEnabled
	common.DebugEnabled = enabled
	t.Cleanup(func() {
		common.DebugEnabled = oldDebug
	})
}
