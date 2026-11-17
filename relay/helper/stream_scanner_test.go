package helper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type headerCommitWriter struct {
	*httptest.ResponseRecorder
	headerStarted chan struct{}
	once          sync.Once
}

func (w *headerCommitWriter) WriteHeader(code int) {
	// Let the scanner start while the first keepalive snapshots the headers.
	w.once.Do(func() { close(w.headerStarted) })
	w.ResponseRecorder.WriteHeader(code)
}

func TestStreamScannerHandler_RequestKeepAliveHandoff(t *testing.T) {
	settings := operation_setting.GetGeneralSetting()
	previousEnabled, previousInterval := settings.PingIntervalEnabled, settings.PingIntervalSeconds
	settings.PingIntervalEnabled, settings.PingIntervalSeconds = true, 60
	t.Cleanup(func() {
		settings.PingIntervalEnabled, settings.PingIntervalSeconds = previousEnabled, previousInterval
	})

	for _, test := range []struct {
		name        string
		interval    time.Duration
		waitForPing bool
	}{
		{name: "before first keepalive", interval: time.Hour},
		{name: "during header commit", interval: time.Millisecond, waitForPing: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &headerCommitWriter{
				ResponseRecorder: httptest.NewRecorder(),
				headerStarted:    make(chan struct{}),
			}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			stop := StartStreamKeepAlive(c, test.interval)
			defer stop()
			if test.waitForPing {
				select {
				case <-writer.headerStarted:
				case <-time.After(time.Second):
					t.Fatal("keepalive did not start")
				}
			}

			resp := &http.Response{
				Header: http.Header{
					"X-Reasoning-Included": {"true"},
					"X-Codex-Turn-State":   {"turn-state"},
				},
				Body: io.NopCloser(strings.NewReader("data: chunk\n\ndata: [DONE]\n\n")),
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			var received []string
			StreamScannerHandler(c, resp, info, func(data string, _ *StreamResult) {
				received = append(received, data)
				assert.NoError(t, StringData(c, data))
			})
			stop()

			require.NotNil(t, info.StreamStatus)
			assert.True(t, info.StreamStatus.IsNormalEnd())
			assert.Equal(t, []string{"chunk"}, received)
			assert.True(t, strings.HasSuffix(writer.Body.String(), "data: chunk\n\n"))
			if test.waitForPing {
				assert.True(t, strings.HasPrefix(writer.Body.String(), ": PING\n\n"))
			} else {
				assert.Equal(t, "data: chunk\n\n", writer.Body.String())
				assert.Equal(t, "true", writer.Result().Header.Get("X-Reasoning-Included"))
				assert.Equal(t, "turn-state", writer.Result().Header.Get("X-Codex-Turn-State"))
			}
		})
	}
}

func init() {
	gin.SetMode(gin.TestMode)
	if constant.StreamingTimeout == 0 {
		constant.StreamingTimeout = 30
	}
}

func setupStreamTest(t *testing.T, body io.Reader) (*gin.Context, *http.Response, *relaycommon.RelayInfo) {
	t.Helper()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{
		Body: io.NopCloser(body),
	}

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	return c, resp, info
}

func buildSSEBody(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "data: {\"id\":%d,\"choices\":[{\"delta\":{\"content\":\"token_%d\"}}]}\n", i, i)
	}
	b.WriteString("data: [DONE]\n")
	return b.String()
}

// ---------- Basic correctness ----------

func TestStreamScannerHandler_NilInputs(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	StreamScannerHandler(c, nil, info, func(data string, sr *StreamResult) {})
	StreamScannerHandler(c, &http.Response{Body: io.NopCloser(strings.NewReader(""))}, info, nil)
}

func TestNewStreamScanner_AllowsLargeStreamLine(t *testing.T) {
	oldBufferMB := constant.StreamScannerMaxBufferMB
	constant.StreamScannerMaxBufferMB = 1
	t.Cleanup(func() {
		constant.StreamScannerMaxBufferMB = oldBufferMB
	})

	payload := strings.Repeat("x", 128<<10)
	scanner := NewStreamScanner(strings.NewReader("data: " + payload + "\n"))
	scanner.Split(bufio.ScanLines)

	require.True(t, scanner.Scan())
	assert.Equal(t, "data: "+payload, scanner.Text())
	require.NoError(t, scanner.Err())
}

func TestStreamScannerHandler_EmptyBody(t *testing.T) {
	t.Parallel()

	c, resp, info := setupStreamTest(t, strings.NewReader(""))

	var called atomic.Bool
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		called.Store(true)
	})

	assert.False(t, called.Load(), "handler should not be called for empty body")
}

func TestStreamScannerHandler_1000Chunks(t *testing.T) {
	t.Parallel()

	const numChunks = 1000
	body := buildSSEBody(numChunks)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		count.Add(1)
	})

	assert.Equal(t, int64(numChunks), count.Load())
	assert.Equal(t, numChunks, info.ReceivedResponseCount)
}

func TestStreamScannerHandler_OrderPreserved(t *testing.T) {
	t.Parallel()

	const numChunks = 500
	body := buildSSEBody(numChunks)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	var mu sync.Mutex
	received := make([]string, 0, numChunks)

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		mu.Lock()
		received = append(received, data)
		mu.Unlock()
	})

	require.Equal(t, numChunks, len(received))
	for i := range numChunks {
		expected := fmt.Sprintf("{\"id\":%d,\"choices\":[{\"delta\":{\"content\":\"token_%d\"}}]}", i, i)
		assert.Equal(t, expected, received[i], "chunk %d out of order", i)
	}
}

func TestStreamScannerHandler_DoneStopsScanner(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		line string
		want []string
	}{
		{name: "bare", line: "[DONE]", want: []string{"before"}},
		{name: "bare with whitespace", line: " \t[DONE] \t\r", want: []string{"before"}},
		{name: "data prefixed", line: "data: [DONE]", want: []string{"before"}},
		{name: "data without space", line: "data:[DONE]", want: []string{"before"}},
		{name: "data with whitespace", line: "data: \t[DONE] \t\r", want: []string{"before"}},
		{name: "bare prefix is not done", line: "[DONE]extra", want: []string{"before", "after"}},
		{name: "data prefix is not done", line: "data: [DONE]extra", want: []string{"before", "[DONE]extra", "after"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := "data: before\n" + test.line + "\ndata: after\ndata: [DONE]\n"
			c, resp, info := setupStreamTest(t, strings.NewReader(body))
			info.DisablePing = true

			var received []string
			StreamScannerHandler(c, resp, info, func(data string, _ *StreamResult) {
				received = append(received, data)
			})

			assert.Equal(t, test.want, received)
			require.NotNil(t, info.StreamStatus)
			assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
			assert.NoError(t, info.StreamStatus.EndError)
			assert.Equal(t, len(test.want), info.ReceivedResponseCount)
		})
	}
}

func TestStreamScannerHandler_StopStopsStream(t *testing.T) {
	t.Parallel()

	const numChunks = 200
	body := buildSSEBody(numChunks)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	const stopAt int64 = 50
	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		n := count.Add(1)
		if n >= stopAt {
			sr.Stop(fmt.Errorf("fatal at %d", n))
		}
	})

	assert.Equal(t, stopAt, count.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
}

func TestStreamScannerHandler_SkipsNonDataLines(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	b.WriteString(": comment line\n")
	b.WriteString("event: message\n")
	b.WriteString("id: 12345\n")
	b.WriteString("retry: 5000\n")
	for i := range 100 {
		fmt.Fprintf(&b, "data: payload_%d\n", i)
		b.WriteString(": interleaved comment\n")
	}
	b.WriteString("data: [DONE]\n")

	c, resp, info := setupStreamTest(t, strings.NewReader(b.String()))

	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		count.Add(1)
	})

	assert.Equal(t, int64(100), count.Load())
}

func TestStreamScannerHandler_DataWithExtraSpaces(t *testing.T) {
	t.Parallel()

	body := "data:   {\"trimmed\":true}  \ndata: [DONE]\n"
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	var got string
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		got = data
	})

	assert.Equal(t, "{\"trimmed\":true}", got)
}

// Disconnect stops downstream writes, while late errors and usage still pass
// through protocol handlers before every goroutine is joined.
func TestStreamScannerHandler_ClientCancelDrainsUpstreamAndReturns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pr.Close()
		_ = pw.Close()
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)

	resp := &http.Response{Body: pr}
	info := &relaycommon.RelayInfo{
		DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	var count atomic.Int64
	firstHandled := make(chan struct{})
	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
			count.Add(1)
			_ = StringData(c, data)
			if data == "first" {
				close(firstHandled)
			}
		})
		close(done)
	}()

	_, err := fmt.Fprint(pw, "data: first\n")
	require.NoError(t, err)

	select {
	case <-firstHandled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first chunk")
	}

	cancel()

	_, err = fmt.Fprint(pw, "event: error\ndata: {\"message\":\"late upstream failure\"}\n\n")
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after client disconnect")
	}

	_, err = fmt.Fprint(pw, "data: second\n")
	require.ErrorIs(t, err, io.ErrClosedPipe, "upstream body must be closed after draining")

	assert.Equal(t, int64(2), count.Load(), "late data must still be parsed")
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.Equal(t, "late upstream failure", info.StreamStatus.UpstreamErrorMessage())
	assert.ErrorIs(t, c.Request.Context().Err(), context.Canceled, "restore the original request before settlement")

	body := recorder.Body.String()
	assert.Contains(t, body, "first")
	assert.NotContains(t, body, "second")
	assert.NotContains(t, body, "late upstream failure")
}

// ---------- Ping tests ----------

func TestStreamScannerHandler_PingSentDuringSlowUpstream(t *testing.T) {
	setting := operation_setting.GetGeneralSetting()
	oldEnabled := setting.PingIntervalEnabled
	oldSeconds := setting.PingIntervalSeconds
	setting.PingIntervalEnabled = true
	setting.PingIntervalSeconds = 1
	t.Cleanup(func() {
		setting.PingIntervalEnabled = oldEnabled
		setting.PingIntervalSeconds = oldSeconds
	})

	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		for i := range 4 {
			fmt.Fprintf(pw, "data: chunk_%d\n", i)
			time.Sleep(400 * time.Millisecond)
		}
		fmt.Fprint(pw, "data: [DONE]\n")
	}()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{Body: pr}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	var count atomic.Int64
	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
			count.Add(1)
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream to finish")
	}

	assert.Equal(t, int64(4), count.Load())

	body := recorder.Body.String()
	pingCount := strings.Count(body, ": PING")
	assert.GreaterOrEqual(t, pingCount, 1,
		"expected at least 1 ping during slow stream with 1s interval; got %d", pingCount)
}

func TestStreamScannerHandler_PingDisabledByRelayInfo(t *testing.T) {
	setting := operation_setting.GetGeneralSetting()
	oldEnabled := setting.PingIntervalEnabled
	oldSeconds := setting.PingIntervalSeconds
	setting.PingIntervalEnabled = true
	setting.PingIntervalSeconds = 1
	t.Cleanup(func() {
		setting.PingIntervalEnabled = oldEnabled
		setting.PingIntervalSeconds = oldSeconds
	})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{Body: io.NopCloser(strings.NewReader(buildSSEBody(5)))}
	info := &relaycommon.RelayInfo{
		DisablePing: true,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}

	var count atomic.Int64
	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
			count.Add(1)
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
	}

	assert.Equal(t, int64(5), count.Load())

	body := recorder.Body.String()
	pingCount := strings.Count(body, ": PING")
	assert.Equal(t, 0, pingCount, "pings should be disabled when DisablePing=true")
}

// ---------- StreamStatus integration ----------

func TestStreamScannerHandler_StreamStatus_DoneReason(t *testing.T) {
	t.Parallel()

	body := buildSSEBody(10)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.Nil(t, info.StreamStatus.EndError)
	assert.True(t, info.StreamStatus.IsNormalEnd())
	assert.False(t, info.StreamStatus.HasErrors())
}

func TestStreamScannerHandler_StreamStatus_EOFWithoutDone(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := range 5 {
		fmt.Fprintf(&b, "data: {\"id\":%d}\n", i)
	}
	c, resp, info := setupStreamTest(t, strings.NewReader(b.String()))

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonEOF, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.IsNormalEnd())
}

func TestStreamScannerHandler_StreamStatus_HandlerStop(t *testing.T) {
	t.Parallel()

	body := buildSSEBody(100)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		n := count.Add(1)
		if n >= 10 {
			sr.Stop(fmt.Errorf("stop at 10"))
		}
	})

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.HasErrors())
}

func TestStreamScannerHandler_StreamStatus_HandlerDone(t *testing.T) {
	t.Parallel()

	body := buildSSEBody(20)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		n := count.Add(1)
		if n >= 5 {
			sr.Done()
		}
	})

	assert.Equal(t, int64(5), count.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.False(t, info.StreamStatus.HasErrors())
}

func TestStreamScannerHandler_StreamStatus_Timeout(t *testing.T) {
	// Not parallel: modifies global constant.StreamingTimeout
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 1
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })

	pr, pw := io.Pipe()
	go func() {
		fmt.Fprint(pw, "data: {\"id\":1}\n")
		time.Sleep(2 * time.Second)
		pw.Close()
	}()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	resp := &http.Response{Body: pr}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}

	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream timeout")
	}

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonTimeout, info.StreamStatus.EndReason)
	assert.False(t, info.StreamStatus.IsNormalEnd())
}

func TestStreamScannerHandler_StreamStatus_SoftErrors(t *testing.T) {
	t.Parallel()

	body := buildSSEBody(10)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		sr.Error(fmt.Errorf("soft error for chunk"))
	})

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.True(t, info.StreamStatus.HasErrors())
	assert.Equal(t, 10, info.StreamStatus.TotalErrorCount())
}

func TestStreamScannerHandler_StreamStatus_MultipleErrorsPerChunk(t *testing.T) {
	t.Parallel()

	body := buildSSEBody(5)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		sr.Error(fmt.Errorf("error A"))
		sr.Error(fmt.Errorf("error B"))
	})

	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.Equal(t, 10, info.StreamStatus.TotalErrorCount())
}

func TestStreamScannerHandler_StreamStatus_ErrorThenStop(t *testing.T) {
	t.Parallel()

	// Use a large body without [DONE] to avoid race between scanner's [DONE]
	// and handler's Stop on the sync.Once EndReason.
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "data: {\"id\":%d}\n", i)
	}
	c, resp, info := setupStreamTest(t, strings.NewReader(b.String()))

	var count atomic.Int64
	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
		count.Add(1)
		sr.Error(fmt.Errorf("soft error"))
		sr.Stop(fmt.Errorf("fatal"))
	})

	assert.Equal(t, int64(1), count.Load())
	require.NotNil(t, info.StreamStatus)
	assert.Equal(t, relaycommon.StreamEndReasonHandlerStop, info.StreamStatus.EndReason)
	assert.Equal(t, 2, info.StreamStatus.TotalErrorCount())
}

func TestStreamScannerHandler_StreamStatus_InitializedIfNil(t *testing.T) {
	t.Parallel()

	body := buildSSEBody(1)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	assert.Nil(t, info.StreamStatus)

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})

	assert.NotNil(t, info.StreamStatus)
}

func TestStreamScannerHandler_StreamStatus_ReplacesPreInitialized(t *testing.T) {
	t.Parallel()

	body := buildSSEBody(5)
	c, resp, info := setupStreamTest(t, strings.NewReader(body))

	info.StreamStatus = relaycommon.NewStreamStatus()
	info.StreamStatus.RecordError("pre-existing error")

	StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {})

	assert.Equal(t, relaycommon.StreamEndReasonDone, info.StreamStatus.EndReason)
	assert.Equal(t, 0, info.StreamStatus.TotalErrorCount())
}

func TestNewStreamScannerCallerLimit(t *testing.T) {
	// The smaller buffer must actually constrain a line; a preallocated 64 KiB
	// buffer would otherwise bypass this caller's 1 KiB limit in bufio.Scanner.
	scanner := NewStreamScanner(strings.NewReader(strings.Repeat("x", 2048)+"\n"), 1024)
	assert.False(t, scanner.Scan())
	require.Error(t, scanner.Err())
	scanner = NewStreamScanner(strings.NewReader("data: ok\n"), 1024)
	require.True(t, scanner.Scan())
	assert.Equal(t, "data: ok", scanner.Text())
	require.NoError(t, scanner.Err())
}

func TestStreamScannerHandler_PreservesUpstreamErrorAfterClientCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })
	c, resp, info := setupStreamTest(t, pr)
	c.Request = c.Request.WithContext(ctx)
	resp.Body = pr
	info.IsStream = true
	info.DisablePing = true
	const message = "Selected model is at capacity. Please try a different model."
	captured := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
			captured <- info.StreamStatus.UpstreamErrorMessage()
			cancel()
		})
		close(done)
	}()
	_, err := fmt.Fprintln(pw, `data: {"type":"error","error":{"code":"server_is_overloaded","type":"overloaded_error","param":"model","message":"`+message+`"}}`)
	require.NoError(t, err)
	require.NoError(t, pw.Close())
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not finish after cancellation")
	}
	assert.Equal(t, message, <-captured, "capture must happen before downstream handling")
	assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
	assert.ErrorIs(t, info.StreamStatus.EndError, context.Canceled)
	info.StreamStatus.CaptureUpstreamError(`{"type":"error","message":"context canceled"}`)
	other := service.GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 0, 1).Snapshot()
	stream, ok := other["stream_status"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", stream["status"])
	assert.Equal(t, "client_gone", stream["end_reason"])
	assert.Equal(t, "context canceled", stream["end_error"])
	assert.Equal(t, message, stream["upstream_error"])
	assert.Equal(t, &relaycommon.UpstreamErrorDetails{
		Code: "server_is_overloaded", Type: "overloaded_error", Param: "model", Message: message,
	}, stream["upstream_error_details"])
	assert.NotContains(t, other, "upstream_error")
	errorOther := model.NewLogOther()
	service.AppendRelayErrorLogAdminInfo(c, info, errorOther)
	assert.Equal(t, stream, errorOther.Snapshot()["stream_status"])
}

func TestStreamScannerHandler_CodeOnlyErrorReachesLog(t *testing.T) {
	c, resp, info := setupStreamTest(t, strings.NewReader("event: error\ndata: {\"code\":\"future_provider_error\"}\n\n"))
	info.IsStream = true
	StreamScannerHandler(c, resp, info, func(string, *StreamResult) {})
	other := model.NewLogOther()
	service.AppendRelayErrorLogAdminInfo(c, info, other)
	stream, ok := other.Snapshot()["stream_status"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "error", stream["status"])
	assert.Equal(t, "eof", stream["end_reason"])
	assert.Equal(t, &relaycommon.UpstreamErrorDetails{Code: "future_provider_error"}, stream["upstream_error_details"])
	assert.NotContains(t, stream, "upstream_error", "do not invent a message")
}

func TestStreamScannerHandler_UpstreamErrorEnvelopes(t *testing.T) {
	for _, tc := range []struct{ name, event, message string }{
		{"response.error flat", `{"type":"response.error","message":"at capacity"}`, "at capacity"},
		{"response.error nested", `{"type":"response.error","response":{"error":{"message":"at capacity"}}}`, "at capacity"},
		{"responses flat", `{"type":"error","message":"at capacity"}`, "at capacity"},
		{"claude error", `{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`, "overloaded"},
		{"responses failed", `{"type":"response.failed","response":{"error":{"message":"server overloaded"}}}`, "server overloaded"},
		{"responses done failed", `{"type":"response.done","response":{"status":"failed","error":{"message":"server overloaded"}}}`, "server overloaded"},
		{"bare error", `{"error":{"message":"upstream unavailable"}}`, "upstream unavailable"},
		{"string error", `{"error":"upstream unavailable"}`, "upstream unavailable"},
		{"content is not an error", `{"type":"response.output_text.delta","delta":"Selected model is at capacity"}`, ""},
		{"nested content is not an error", `{"choices":[{"delta":{"content":"overloaded","error":{"message":"fictional"}}}]}`, ""},
		{"echoed channel key", `{"error":{"message":"Incorrect API key provided: channel-secret"}}`, "Incorrect API key provided: ***"},
		{"malformed", `{"type":"error","message":"incomplete"`, ""},
		{"bounded unicode", `{"error":{"message":"` + strings.Repeat("慢", 2100) + `"}}`, strings.Repeat("慢", 2048) + "…"},
		{"masked and normalized", `{"error":{"message":" api_key:secret\nretry\u0000later "}}`, "api_key:*** retry later"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, resp, info := setupStreamTest(t, strings.NewReader("data: "+tc.event+"\ndata: [DONE]\n"))
			info.IsStream = true
			info.ApiKey = "channel-secret"
			info.BeginAttempt()
			StreamScannerHandler(c, resp, info, func(string, *StreamResult) {})
			assert.Equal(t, tc.message, info.StreamStatus.UpstreamErrorMessage())
			other := service.GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 1, 0, 1).Snapshot()
			stream := other["stream_status"].(map[string]any)
			if tc.message == "" {
				assert.False(t, info.FirstResponseTime.IsZero(), "ordinary data must record first response time")
				assert.Equal(t, "ok", stream["status"])
				assert.True(t, service.RequestPolicy(c).Successful)
				assert.NotContains(t, stream, "upstream_error")
			} else {
				assert.True(t, info.FirstResponseTime.IsZero(), "an upstream error must not record first response time")
				assert.Equal(t, "error", stream["status"])
				assert.Equal(t, "done", stream["end_reason"])
				assert.False(t, service.RequestPolicy(c).Successful)
			}
		})
	}
}

// Cancel from the read boundary, after bytes arrive but before Scan returns.
type cancelOnRead struct {
	io.Reader
	cancel context.CancelFunc
	reads  int
	closed atomic.Bool
}

func (r *cancelOnRead) Read(p []byte) (int, error) {
	r.reads++
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func (r *cancelOnRead) Close() error {
	r.closed.Store(true)
	return nil
}

func TestStreamScannerHandler_PreservesAlreadyReadErrorOnCancel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		buffer  string
		unread  string
		message string
	}{
		{"typed error", "data: {\"type\":\"response.error\",\"message\":\"at capacity\"}\n", "", "at capacity"},
		{"named error", "event: error\ndata: {\"message\":\"at capacity\"}\n\n", "", "at capacity"},
		{"CRLF named error", "event: response.error\r\ndata: {\"message\":\"overloaded\"}\r\n\r\n", "", "overloaded"},
		{"multiline error", "event: error\ndata: {\ndata: \"message\":\"overloaded\"}\n\n", "", "overloaded"},
		{"content before error", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\nevent: error\ndata: {\"message\":\"overloaded\"}\n\n", "", "overloaded"},
		{"event type reset", "event: error\n\ndata: {\"message\":\"ordinary content\"}\n\n", "", ""},
		{"late error", "event: error\n", "data: {\"message\":\"late failure\"}\n\n", "late failure"},
		{"final line without newline", "event: error\ndata: {\"message\":\"unfinished\"}", "", "unfinished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &cancelOnRead{
				Reader: io.MultiReader(strings.NewReader(tc.buffer), strings.NewReader(tc.unread)),
				cancel: cancel,
			}
			c, resp, info := setupStreamTest(t, body)
			resp.Body = body
			c.Request = c.Request.WithContext(ctx)
			info.DisablePing = true
			var forwarded atomic.Int64
			StreamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
				forwarded.Add(1)
				require.NoError(t, StringData(c, data))
			})
			assert.Equal(t, tc.message, info.StreamStatus.UpstreamErrorMessage())
			assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
			assert.ErrorIs(t, info.StreamStatus.EndError, context.Canceled)
			assert.Positive(t, forwarded.Load(), "late payloads still reach the protocol parser")
			assert.False(t, c.Writer.Written(), "must not write to the canceled client")
			assert.True(t, body.closed.Load(), "must close upstream after draining")
		})
	}
}

type streamCancelReadError struct {
	cancel context.CancelFunc
	err    error
}

func (r *streamCancelReadError) Read([]byte) (int, error) {
	r.cancel()
	return 0, r.err
}

func TestStreamScannerHandler_ReadErrorSurvivesClientCancel(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		err        error
	}{
		{"HTTP2 reset", "upstream_http2_stream_error", errors.New("stream error: stream ID 1; INTERNAL_ERROR; received from peer")},
		{"unexpected EOF", "upstream_stream_read_error", io.ErrUnexpectedEOF},
		{"clean EOF without terminal", "upstream_stream_truncated", io.EOF},
		{"cancellation has no upstream cause", "", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, resp, info := setupStreamTest(t, &streamCancelReadError{cancel: cancel, err: tc.err})
			c.Request = c.Request.WithContext(ctx)
			info.IsStream, info.DisablePing = true, true
			StreamScannerHandler(c, resp, info, func(string, *StreamResult) {})
			info.StreamStatus.RequireTerminal()
			other := model.NewLogOther()
			service.AppendRelayErrorLogAdminInfo(c, info, other)
			stream := other.Snapshot()["stream_status"].(map[string]any)
			assert.Equal(t, true, stream["client_disconnected"])
			if tc.code == "" {
				assert.Equal(t, "client_gone", stream["end_reason"])
				assert.NotContains(t, stream, "upstream_error_details")
				return
			}
			if errors.Is(tc.err, io.EOF) {
				assert.Equal(t, "client_gone", stream["end_reason"])
			} else {
				assert.Equal(t, "scanner_error", stream["end_reason"])
				assert.Equal(t, tc.err.Error(), stream["end_error"])
			}
			details, ok := stream["upstream_error_details"].(*relaycommon.UpstreamErrorDetails)
			require.True(t, ok)
			assert.Equal(t, tc.code, details.Code)
		})
	}
}

func TestStreamScannerHandler_DrainDeadlineClosesBlockedUpstream(t *testing.T) {
	for _, timeout := range []time.Duration{0, time.Millisecond} {
		t.Run(timeout.String(), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			c, resp, info := setupStreamTest(t, reader)
			resp.Body = reader
			c.Request = c.Request.WithContext(ctx)
			info.IsStream, info.DisablePing = true, true
			done := make(chan struct{})
			go func() {
				streamScannerHandler(c, resp, info, func(string, *StreamResult) {}, timeout)
				close(done)
			}()
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("drain deadline did not release the blocked read")
			}
			_, err := writer.Write([]byte("data: late\n\n"))
			require.ErrorIs(t, err, io.ErrClosedPipe)
			diagnostics := info.StreamStatus.DiagnosticsSnapshot()
			assert.ErrorIs(t, diagnostics.ClientError, context.Canceled)
			assert.Equal(t, timeout > 0, diagnostics.DrainTimedOut)
			assert.NoError(t, diagnostics.UpstreamReadError, "closing our own body must not fabricate an upstream failure")
			assert.False(t, diagnostics.UpstreamEOF, "closing our own body is not natural EOF")
			other := model.NewLogOther()
			service.AppendRelayErrorLogAdminInfo(c, info, other)
			stream := other.Snapshot()["stream_status"].(map[string]any)
			assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
			assert.Equal(t, timeout > 0, stream["drain_timeout"] == true)
			assert.NotContains(t, stream, "upstream_error_details")
		})
	}
}

func TestStreamScannerHandler_DrainStopsAtTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, frame, errorMessage string
		outcome                   relaycommon.ResponseOutcome
		cancelAfterTerminal       bool
	}{
		{name: "named upstream error", frame: "event: error\ndata: {\"message\":\"late failure\"}\n\n", errorMessage: "late failure"},
		{name: "multiline upstream error", frame: "event: error\ndata: {\ndata: \"message\":\"late failure\"}\n\n", errorMessage: "late failure"},
		{name: "completed", frame: "data: terminal\n\n", outcome: relaycommon.ResponseOutcomeCompleted},
		{name: "failed", frame: "data: terminal\n\n", outcome: relaycommon.ResponseOutcomeFailed},
		{name: "error before disconnect", frame: "event: error\ndata: {\"message\":\"late failure\"}\n\n", errorMessage: "late failure", cancelAfterTerminal: true},
		{name: "completion before disconnect", frame: "data: terminal\n\n", outcome: relaycommon.ResponseOutcomeCompleted, cancelAfterTerminal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			reader, writer := io.Pipe()
			t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
			c, resp, info := setupStreamTest(t, reader)
			resp.Body = reader
			c.Request = c.Request.WithContext(ctx)
			info.DisablePing = true
			done := make(chan struct{})
			firstHandled := make(chan struct{})
			terminalHandled := make(chan struct{})
			var parsed []string
			go func() {
				streamScannerHandler(c, resp, info, func(data string, sr *StreamResult) {
					parsed = append(parsed, data)
					if data == "first" {
						if !tc.cancelAfterTerminal {
							cancel()
						}
						close(firstHandled)
					} else if tc.outcome == relaycommon.ResponseOutcomeCompleted {
						info.StreamStatus.MarkCompleted()
					} else if tc.outcome == relaycommon.ResponseOutcomeFailed {
						info.StreamStatus.MarkFailed("server_error", "upstream_error", 503)
					}
					if tc.cancelAfterTerminal && data != "first" {
						close(terminalHandled)
					}
				}, time.Hour)
				close(done)
			}()
			_, err := fmt.Fprint(writer, "data: first\n\n")
			require.NoError(t, err)
			select {
			case <-firstHandled:
			case <-time.After(time.Second):
				t.Fatal("first frame was not processed")
			}
			_, err = io.WriteString(writer, tc.frame)
			require.NoError(t, err)
			if tc.cancelAfterTerminal {
				select {
				case <-terminalHandled:
				case <-time.After(time.Second):
					t.Fatal("terminal frame was not processed")
				}
				cancel()
			}
			// Keep upstream open: the terminal result itself must end the drain.
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("terminal result did not close upstream")
			}
			assert.GreaterOrEqual(t, len(parsed), 2, "parse the final frame before returning")
			assert.Equal(t, tc.errorMessage, info.StreamStatus.UpstreamErrorMessage())
			assert.Equal(t, string(tc.outcome), info.StreamStatus.ResponseOutcome())
			assert.Equal(t, relaycommon.StreamEndReasonClientGone, info.StreamStatus.EndReason)
			assert.False(t, info.StreamStatus.DiagnosticsSnapshot().DrainTimedOut)
			_, err = io.WriteString(writer, "data: unwanted generation\n\n")
			assert.ErrorIs(t, err, io.ErrClosedPipe)
		})
	}
}

func TestStreamUpstreamContextDrainLifetime(t *testing.T) {
	oldDrain, oldIdle := constant.StreamDrainTimeout, constant.StreamingTimeout
	t.Cleanup(func() { constant.StreamDrainTimeout, constant.StreamingTimeout = oldDrain, oldIdle })
	for _, tc := range []struct {
		name             string
		drain, idle      int
		deadline, budget time.Duration
	}{
		{name: "zero closes immediately", drain: 0, idle: 30},
		{name: "three-second window", drain: 3, idle: 30, budget: 3 * time.Second},
		{name: "negative uses default", drain: -1, idle: 30, budget: 3 * time.Second},
		{name: "shorter idle timeout", drain: 2, idle: 1, budget: time.Second},
		{name: "provider deadline", drain: 2, idle: 30, deadline: 250 * time.Millisecond, budget: 250 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constant.StreamDrainTimeout, constant.StreamingTimeout = tc.drain, tc.idle
			synctest.Test(t, func(t *testing.T) {
				client, cancel := context.WithCancel(context.Background())
				defer cancel()
				type requestValueKey struct{}
				upstream := context.WithValue(context.Background(), requestValueKey{}, "request-value")
				if tc.deadline != 0 {
					var release context.CancelFunc
					upstream, release = context.WithTimeout(upstream, tc.deadline)
					defer release()
				}
				ctx, release := NewStreamUpstreamContext(client, upstream)
				defer release()
				assert.Equal(t, "request-value", ctx.Value(requestValueKey{}))
				cancel()
				synctest.Wait()
				if tc.budget == 0 {
					assert.ErrorIs(t, ctx.Err(), context.Canceled, "zero disables the drain")
					assert.NotErrorIs(t, context.Cause(ctx), errStreamDrainTimeout)
					return
				}
				assert.NoError(t, ctx.Err(), "client cancellation starts a bounded drain")
				time.Sleep(tc.budget - time.Nanosecond)
				assert.NoError(t, ctx.Err())
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				if tc.deadline != 0 {
					assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "preserve the provider deadline")
					return
				}
				assert.ErrorIs(t, ctx.Err(), context.Canceled, "drain expires at the absolute budget")
				assert.ErrorIs(t, context.Cause(ctx), errStreamDrainTimeout)
			})
		})
	}
}

func TestStreamScannerHandlerNamedErrorsReachLog(t *testing.T) {
	for _, tc := range []struct{ name, body, message string }{
		{"named error", "event: error\ndata: {\"message\":\"at capacity\"}\n\n", "at capacity"},
		{"named response error", "event: response.error\ndata: {\"message\":\"overloaded\"}\n\n", "overloaded"},
		{"multiline named error", "event: error\ndata: {\ndata: \"message\":\"overloaded\"}\n\n", "overloaded"},
		{"nested untyped error", "data: {\"response\":{\"error\":{\"message\":\"overloaded\"}}}\n\n", "overloaded"},
		{"event reset", "event: error\n\ndata: {\"message\":\"ordinary content\"}\n\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, resp, info := setupStreamTest(t, strings.NewReader(tc.body+"data: [DONE]\n"))
			info.IsStream = true
			StreamScannerHandler(c, resp, info, func(string, *StreamResult) {})
			other := model.NewLogOther()
			service.AppendRelayErrorLogAdminInfo(c, info, other)
			stream, ok := other.Snapshot()["stream_status"].(map[string]any)
			require.True(t, ok)
			if tc.message == "" {
				assert.NotContains(t, stream, "upstream_error")
			} else {
				assert.Equal(t, tc.message, stream["upstream_error"])
			}
		})
	}
}

// ---------- Write deadlines ----------

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (r *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	r.deadlines = append(r.deadlines, deadline)
	return nil
}

func TestStreamWriteBudget(t *testing.T) {
	start := time.Unix(1_800_000_000, 0)
	var budget StreamWriteBudget
	steps := []struct {
		name  string
		at    time.Duration
		bytes int
		want  time.Duration
	}{
		{"small event", 0, 8 << 10, 31 * time.Second},
		{"image event gets the time a slow client needs", 0, 3600 << 10, 481 * time.Second},
		{"next write keeps the image budget", 60 * time.Second, 8 << 10, 482 * time.Second},
		{"budget starts over once it ran out", 600 * time.Second, 8 << 10, 631 * time.Second},
	}
	for _, step := range steps {
		assert.Equal(t, start.Add(step.want), budget.Deadline(start.Add(step.at), step.bytes), step.name)
	}
}

func TestSetEventStreamHeadersBoundsStreamWritesByBudget(t *testing.T) {
	recorder := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	SetEventStreamHeaders(c)

	before := time.Now()
	require.NoError(t, StringData(c, strings.Repeat("A", 3600<<10)))

	require.NotEmpty(t, recorder.deadlines)
	assert.False(t, recorder.deadlines[0].Before(before.Add(streamWriteTimeout+450*time.Second)))
	assert.Contains(t, recorder.Body.String(), "data: AAAA")
}
