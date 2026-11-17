package helper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
)

const (
	InitialScannerBufferSize    = 64 << 10  // 64KB (64*1024)
	DefaultMaxScannerBufferSize = 128 << 20 // 64MB (64*1024*1024) default SSE buffer size
	DefaultPingInterval         = 10 * time.Second
	// streamWriteTimeout bounds a write blocked on a client that stopped
	// reading so the unconditional wg.Wait() in cleanup can always finish.
	// Without it, a connected client with a full TCP buffer (no server
	// WriteTimeout) could hang the handler forever. StreamWriteBudget adds the
	// time a client reading at streamMinClientRate needs for the data.
	streamWriteTimeout  = 30 * time.Second
	streamMinClientRate = 8 << 10 // bytes per second
)

func getScannerBufferSize() int {
	if constant.StreamScannerMaxBufferMB > 0 {
		return constant.StreamScannerMaxBufferMB << 20
	}
	return DefaultMaxScannerBufferSize
}

// NewStreamScanner shares relay scanner configuration. Callers buffering bounded
// task state may additionally cap a line without increasing the configured limit.
func NewStreamScanner(reader io.Reader, maxBytes ...int) *bufio.Scanner {
	limit := getScannerBufferSize()
	if len(maxBytes) > 0 && maxBytes[0] > 0 {
		limit = min(limit, maxBytes[0])
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, min(InitialScannerBufferSize, limit)), limit)
	return scanner
}

type upstreamStreamReader struct {
	io.Reader
	ctx    context.Context
	status *relaycommon.StreamStatus
}

func (r *upstreamStreamReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if r.ctx.Err() == nil {
		r.status.CaptureUpstreamReadError(err)
	}
	return n, err
}

// streamDrainWriter lets protocol handlers continue collecting final usage
// without writing to a disconnected client or treating write failure as an
// upstream failure. Only this writer and the main loop observe clientContext;
// the parser runs with request values and an independent cancellation lifetime.
type streamDrainWriter struct {
	gin.ResponseWriter
	clientContext  context.Context
	status         *relaycommon.StreamStatus
	disconnected   chan struct{}
	disconnectOnce sync.Once
	discarded      atomic.Bool
}

func (w *streamDrainWriter) disconnect(err error) {
	w.disconnectOnce.Do(func() {
		w.status.MarkClientDisconnected(err)
		close(w.disconnected)
	})
}

func (w *streamDrainWriter) clientDisconnected() bool {
	if w.clientContext.Err() != nil {
		return true
	}
	select {
	case <-w.disconnected:
		return true
	default:
		return false
	}
}

func (w *streamDrainWriter) discard() bool {
	if err := w.clientContext.Err(); err != nil {
		if errors.Is(context.Cause(w.clientContext), relaycommon.ErrStreamAborted) {
			w.discarded.Store(true)
			return true
		}
		w.disconnect(err)
	}
	select {
	case <-w.disconnected:
		w.discarded.Store(true)
		return true
	default:
		return false
	}
}

func (w *streamDrainWriter) Write(p []byte) (int, error) {
	if !w.discard() {
		n, err := w.ResponseWriter.Write(p)
		if err == nil && n == len(p) {
			return n, nil
		}
		if err == nil {
			err = io.ErrShortWrite
		}
		w.discarded.Store(true)
		w.disconnect(err)
	}
	return len(p), nil
}

func (w *streamDrainWriter) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func (w *streamDrainWriter) WriteHeader(code int) {
	if !w.discard() {
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *streamDrainWriter) WriteHeaderNow() {
	if !w.discard() {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *streamDrainWriter) Flush() {
	if !w.discard() {
		if err := flushResponseWriter(w.ResponseWriter); err != nil {
			w.discarded.Store(true)
			w.disconnect(err)
		}
	}
}

// Downstream errors have already been recorded; the parser must keep running.
func (w *streamDrainWriter) FlushError() error { return nil }

func (w *streamDrainWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func copyCodexSSEHeaders(c *gin.Context, resp *http.Response) {
	if c == nil || c.Writer == nil || resp == nil {
		return
	}
	// codex
	for _, name := range []string{"X-Reasoning-Included", "X-Codex-Turn-State"} {
		values := resp.Header.Values(name)
		if !service.ShouldCopyUpstreamHeader(c, name, values) {
			continue
		}
		for _, value := range values {
			if value != "" {
				c.Writer.Header().Add(name, value)
			}
		}
	}
}

// StreamWriteBudget gives the writes of one stream a deadline of
// streamWriteTimeout past the time a client reading at streamMinClientRate
// needs for everything written so far. A fixed deadline per write cut slow
// clients off in the middle of a multi-megabyte image event, and the timeout
// looked like a client disconnect. The budget carries over between writes
// because the kernel can still hold megabytes of earlier writes and wakes a
// blocked writer only after a large part of them drains. A client that stops
// reading is still cut off once the budget runs out. Callers serialize writes.
type StreamWriteBudget struct {
	drainBy time.Time
}

// Deadline returns the write deadline for n more bytes written at now.
func (b *StreamWriteBudget) Deadline(now time.Time, n int) time.Time {
	if b.drainBy.Before(now) {
		b.drainBy = now
	}
	b.drainBy = b.drainBy.Add(time.Duration(n) * time.Second / streamMinClientRate)
	return b.drainBy.Add(streamWriteTimeout)
}

// streamResponseWriter sets the connection write deadline from its budget
// before every write. Writers that don't support deadlines (e.g. httptest
// recorders) are silently ignored.
type streamResponseWriter struct {
	gin.ResponseWriter
	budget StreamWriteBudget
}

func (w *streamResponseWriter) Write(p []byte) (int, error) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(w.budget.Deadline(time.Now(), len(p)))
	return w.ResponseWriter.Write(p)
}

func (w *streamResponseWriter) WriteString(s string) (int, error) {
	_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(w.budget.Deadline(time.Now(), len(s)))
	return w.ResponseWriter.WriteString(s)
}

// Unwrap lets http.ResponseController reach the connection.
func (w *streamResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func StreamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string, sr *StreamResult)) {
	streamScannerHandler(c, resp, info, dataHandler, StreamDrainTimeout())
}

func streamScannerHandler(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, dataHandler func(data string, sr *StreamResult), drainTimeout time.Duration) {

	if resp == nil || dataHandler == nil {
		return
	}

	// Finish the request keepalive before changing headers. The scanner owns
	// subsequent ping and data writes through its shared write mutex.
	if writer := keepAliveWriter(c); writer != nil {
		writer.stop()
	}

	// 无条件新建 StreamStatus
	info.StreamStatus = relaycommon.NewStreamStatus()

	ctx, cancel := context.WithCancel(context.Background())
	clientRequest := c.Request
	clientContext := clientRequest.Context()

	streamingTimeout := time.Duration(constant.StreamingTimeout) * time.Second

	var (
		stopChan    = make(chan bool, 3) // 增加缓冲区避免阻塞
		scanner     = NewStreamScanner(&upstreamStreamReader{Reader: resp.Body, ctx: ctx, status: info.StreamStatus})
		ticker      = time.NewTicker(streamingTimeout)
		pingTicker  *time.Ticker
		writeMutex  sync.Mutex     // Mutex to protect concurrent writes
		wg          sync.WaitGroup // 用于等待所有 goroutine 退出
		cleanupOnce sync.Once
		stopOnce    sync.Once
	)

	stop := func() {
		stopOnce.Do(func() {
			close(stopChan)
		})
	}

	upstreamKey := ""
	if info.ChannelMeta != nil {
		upstreamKey = info.ApiKey
	}
	generalSettings := operation_setting.GetGeneralSetting()
	pingEnabled := generalSettings.PingIntervalEnabled && !info.DisablePing
	pingInterval := time.Duration(generalSettings.PingIntervalSeconds) * time.Second
	if pingInterval <= 0 {
		pingInterval = DefaultPingInterval
	}

	if pingEnabled {
		pingTicker = time.NewTicker(pingInterval)
	}

	logger.LogDebug(c, "relay timeout seconds: %d", common.RelayTimeout)
	logger.LogDebug(c, "relay max idle conns: %d", common.RelayMaxIdleConns)
	logger.LogDebug(c, "relay max idle conns per host: %d", common.RelayMaxIdleConnsPerHost)
	logger.LogDebug(c, "streaming timeout seconds: %d", int64(streamingTimeout.Seconds()))
	logger.LogDebug(c, "ping interval seconds: %d", int64(pingInterval.Seconds()))

	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel()
			stop()
			if resp.Body != nil {
				_ = resp.Body.Close()
			}

			ticker.Stop()
			if pingTicker != nil {
				pingTicker.Stop()
			}

			wg.Wait()
		})
	}
	// Ensure gin.Context is not returned to Gin's pool while any stream goroutine can still use it.
	defer func() {
		cleanup()
		c.Request = clientRequest
	}()

	copyCodexSSEHeaders(c, resp)
	SetEventStreamHeaders(c)
	downstream := &streamDrainWriter{
		ResponseWriter: c.Writer, clientContext: clientContext,
		status: info.StreamStatus, disconnected: make(chan struct{}),
	}
	c.Writer = downstream
	// Existing adaptors combine protocol/usage parsing with response writes.
	// Detach only while they run; the writer owns downstream cancellation, and
	// callers regain the original request before settlement or retry decisions.
	c.Request = clientRequest.WithContext(context.WithoutCancel(clientContext))

	ctx = context.WithValue(ctx, "stop_chan", stopChan)

	// Handle ping data sending with improved error handling
	if pingEnabled && pingTicker != nil {
		wg.Add(1)
		gopool.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					logger.LogError(c, common.LogText("ping goroutine panic: %v", r))
					info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("ping panic: %v", r))
					stop()
				}
				logger.LogDebug(c, "ping goroutine exited")
				wg.Done()
			}()

			// 添加超时保护，防止 goroutine 无限运行
			maxPingDuration := 30 * time.Minute // 最大 ping 持续时间
			pingTimeout := time.NewTimer(maxPingDuration)
			defer pingTimeout.Stop()

			for {
				select {
				case <-pingTicker.C:
					var err error
					func() {
						writeMutex.Lock()
						defer writeMutex.Unlock()
						err = PingData(c)
					}()
					if err != nil {
						logger.LogError(c, common.LogText("ping data error: %s", err.Error()))
						downstream.disconnect(err)
						return
					}
					logger.LogDebug(c, "ping data sent")
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				case <-clientContext.Done():
					// 监听客户端断开连接
					return
				case <-pingTimeout.C:
					logger.LogError(c, common.LogText("ping goroutine max duration reached"))
					return
				}
			}
		})
	}

	type streamFrame struct {
		data          string
		upstreamError bool
	}
	dataChan := make(chan streamFrame, 10)
	var terminalParsed atomic.Bool
	sr := newStreamResult(info.StreamStatus)

	wg.Add(1)
	gopool.Go(func() {
		defer func() {
			if r := recover(); r != nil {
				logger.LogError(c, common.LogText("data handler goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("handler panic: %v", r))
			}
			stop()
			wg.Done()
		}()
		for frame := range dataChan {
			sr.reset()
			if frame.data != "" {
				func() {
					writeMutex.Lock()
					defer writeMutex.Unlock()
					dataHandler(frame.data, sr)
				}()
			}
			if frame.upstreamError || info.StreamStatus.ResponseOutcome() != "" {
				terminalParsed.Store(true)
			}
			if sr.IsStopped() || downstream.clientDisconnected() && terminalParsed.Load() {
				return
			}
		}
	})

	// Scanner goroutine with improved error handling
	wg.Add(1)
	common.RelayCtxGo(ctx, func() {
		defer func() {
			close(dataChan)
			if r := recover(); r != nil {
				logger.LogError(c, common.LogText("scanner goroutine panic: %v", r))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonPanic, fmt.Errorf("scanner panic: %v", r))
			}
			stop()
			logger.LogDebug(c, "scanner goroutine exited")
			wg.Done()
		}()

		eventType := ""
		var eventData strings.Builder
		for scanner.Scan() {
			data := scanner.Text()
			if data == "" {
				info.StreamStatus.CaptureUpstreamSSEError(eventData.String(), eventType, upstreamKey)
				eventData.Reset()
				eventType = ""
			} else if event, ok := strings.CutPrefix(data, "event:"); ok {
				eventType = strings.TrimSpace(event)
			}
			// Capture diagnostics before handing the frame to protocol handlers.
			if payload, ok := strings.CutPrefix(data, "data:"); ok {
				info.StreamStatus.CaptureUpstreamSSEError(strings.TrimSpace(payload), eventType, upstreamKey)
				// SSE joins multiple data fields with newlines. Keep diagnostics
				// bounded by the same limit as the scanner and stop after capture.
				if info.StreamStatus.UpstreamErrorMessage() == "" && eventData.Len() < getScannerBufferSize() {
					payload, _ = strings.CutPrefix(payload, " ")
					eventData.WriteString(payload[:min(len(payload), getScannerBufferSize()-eventData.Len())])
					if eventData.Len() < getScannerBufferSize() {
						eventData.WriteByte('\n')
					}
				}
			}
			// 检查是否需要停止
			select {
			case <-stopChan:
				return
			case <-ctx.Done():
				return
			default:
			}

			ticker.Reset(streamingTimeout)
			logger.LogDebug(c, "stream scanner data: %s", data)

			data, hasDataPrefix := strings.CutPrefix(data, "data:")
			data = strings.TrimSpace(data)
			upstreamError := info.StreamStatus.UpstreamErrorDetails() != nil
			stopAfterFrame := downstream.clientDisconnected() && upstreamError
			if !hasDataPrefix && data != "[DONE]" || data == "" {
				if upstreamError {
					// A multiline error may only be recognized at the delimiter.
					// Queue its marker after the payloads so final usage is parsed first.
					select {
					case dataChan <- streamFrame{upstreamError: true}:
					case <-ctx.Done():
						return
					case <-stopChan:
						return
					}
				}
				if stopAfterFrame {
					return
				}
				continue
			}
			if data != "[DONE]" {
				if clientContext.Err() == nil && info.StreamStatus.UpstreamErrorDetails() == nil {
					info.SetFirstResponseTime()
				}
				info.ReceivedResponseCount++

				select {
				case dataChan <- streamFrame{data: data, upstreamError: upstreamError}:
				case <-ctx.Done():
					return
				case <-stopChan:
					return
				}
			} else {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonDone, nil)
				logger.LogDebug(c, "received [DONE], stopping scanner")
				return
			}
			// The current payload is queued for parsing before closing upstream.
			// An error ends the drain even if the provider leaves the socket open.
			if stopAfterFrame {
				return
			}
		}
		info.StreamStatus.CaptureUpstreamSSEError(eventData.String(), eventType, upstreamKey)

		// Closing the upstream body is part of stopping the scanner. Its EOF or
		// read error must not replace the actual stop reason or add a false error.
		if ctx.Err() != nil {
			return
		}
		if err := scanner.Err(); err != nil {
			if err != io.EOF {
				info.StreamStatus.CaptureUpstreamReadError(err)
				logger.LogError(c, common.LogText("scanner error: %s", err.Error()))
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonScannerErr, err)
			}
		} else {
			info.StreamStatus.MarkUpstreamEOF()
		}
		info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonEOF, nil)
	})

	clientDone := clientContext.Done()
	disconnected := (<-chan struct{})(downstream.disconnected)
	var drainTimer *time.Timer
	var drainDone <-chan time.Time
	waiting := true
	for waiting {
		select {
		case <-ticker.C:
			if drainTimer != nil {
				info.StreamStatus.MarkDrainTimedOut()
			}
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonTimeout, nil)
			waiting = false
		case <-stopChan:
			waiting = false
		case <-clientDone:
			if errors.Is(context.Cause(clientContext), relaycommon.ErrStreamAborted) {
				info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, clientContext.Err())
				waiting = false
				break
			}
			downstream.disconnect(clientContext.Err())
			clientDone = nil
		case <-disconnected:
			diagnostics := info.StreamStatus.DiagnosticsSnapshot()
			info.StreamStatus.SetEndReason(relaycommon.StreamEndReasonClientGone, diagnostics.ClientError)
			if errors.Is(context.Cause(clientContext), relaycommon.ErrStreamAborted) {
				waiting = false
				break
			}
			disconnected, clientDone = nil, nil
			// Unblock a write already in progress; future writes are discarded.
			_ = http.NewResponseController(downstream.ResponseWriter).SetWriteDeadline(time.Now())
			if drainTimeout <= 0 || terminalParsed.Load() {
				waiting = false
				break
			}
			drainTimer = time.NewTimer(drainTimeout)
			drainDone = drainTimer.C
		case <-drainDone:
			info.StreamStatus.MarkDrainTimedOut()
			waiting = false
		}
	}
	if drainTimer != nil {
		drainTimer.Stop()
	}

	cleanup()
	if resp.Request != nil && errors.Is(context.Cause(resp.Request.Context()), errStreamDrainTimeout) {
		info.StreamStatus.MarkDrainTimedOut()
	}
	// All stream goroutines have joined. Done from a successfully delivered
	// protocol completion can now correct a cancellation that won while its
	// callback was still running. Bare EOF and failed writes never qualify.
	if errors.Is(context.Cause(clientContext), relaycommon.ErrStreamAborted) {
		info.StreamStatus.EndReason = relaycommon.StreamEndReasonClientGone
		info.StreamStatus.EndError = clientContext.Err()
	} else if err := clientContext.Err(); err != nil {
		downstream.disconnect(err)
	}
	diagnostics := info.StreamStatus.DiagnosticsSnapshot()
	if diagnostics.UpstreamReadError != nil {
		info.StreamStatus.EndReason = relaycommon.StreamEndReasonScannerErr
		info.StreamStatus.EndError = diagnostics.UpstreamReadError
	} else if diagnostics.ClientError != nil && (info.StreamStatus.EndReason == relaycommon.StreamEndReasonNone || info.StreamStatus.IsNormalEnd() ||
		errors.Is(info.StreamStatus.EndError, context.Canceled) || errors.Is(info.StreamStatus.EndError, context.DeadlineExceeded)) {
		info.StreamStatus.EndReason = relaycommon.StreamEndReasonClientGone
		info.StreamStatus.EndError = diagnostics.ClientError
	}
	if sr.done && !downstream.discarded.Load() && info.StreamStatus.ResponseOutcome() == string(relaycommon.ResponseOutcomeCompleted) &&
		!errors.Is(context.Cause(clientContext), relaycommon.ErrStreamAborted) &&
		info.StreamStatus.EndReason == relaycommon.StreamEndReasonClientGone &&
		errors.Is(info.StreamStatus.EndError, context.Canceled) && !info.StreamStatus.HasErrors() {
		info.StreamStatus.EndReason = relaycommon.StreamEndReasonDone
		info.StreamStatus.EndError = nil
	}
	if info.StreamStatus.IsNormalEnd() && !info.StreamStatus.HasErrors() {
		logger.LogInfo(c, common.LogText("stream ended: %s", info.StreamStatus.Summary()))
	} else {
		logger.LogError(c, common.LogText("stream ended: %s, received=%d", info.StreamStatus.Summary(), info.ReceivedResponseCount))
	}
}
