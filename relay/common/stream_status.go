package common

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

// ErrStreamAborted distinguishes an intentional gateway stop from a vanished
// client. Reply checks must stop generation immediately, without a drain.
var ErrStreamAborted = errors.New("stream stopped by gateway")

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

// ResponseOutcome is the protocol-level result of one response, independent of
// how the transport ended. Adaptors mark it from the events they already parse.
type ResponseOutcome string

const (
	ResponseOutcomeUnknown    ResponseOutcome = ""
	ResponseOutcomeCompleted  ResponseOutcome = "completed"
	ResponseOutcomeFailed     ResponseOutcome = "failed"
	ResponseOutcomeIncomplete ResponseOutcome = "incomplete"
	ResponseOutcomeCancelled  ResponseOutcome = "cancelled"
)

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once

	mu         sync.Mutex
	Errors     []StreamErrorEntry
	ErrorCount int

	response         ResponseOutcome
	errorCode        string
	errorType        string
	errorStatus      int
	incompleteReason string
	expectsTerminal  bool
	upstreamError    *UpstreamErrorDetails
	upstreamReadErr  error
	upstreamEOF      bool
	clientError      error
	drainTimedOut    bool
}

// StreamOutcome holds classification facts only; upstream messages never
// enter it because they may contain credentials or request content.
type StreamOutcome struct {
	EndReason        StreamEndReason
	HasErrors        bool
	ExpectsTerminal  bool
	Response         ResponseOutcome
	ErrorCode        string
	ErrorType        string
	ErrorStatus      int
	IncompleteReason string
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.EndReason = reason
		s.EndError = err
	})
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

// CaptureUpstreamError saves the first actual upstream error before downstream
// writes can cancel the request. It is diagnostic only, not a routing signal.
func (s *StreamStatus) CaptureUpstreamError(data string, secrets ...string) {
	s.CaptureUpstreamSSEError(data, "", secrets...)
}

// CaptureUpstreamSSEError also recognizes errors identified by the SSE event
// field when the JSON payload omits its own type.
func (s *StreamStatus) CaptureUpstreamSSEError(data, eventType string, secrets ...string) {
	if s == nil || s.UpstreamErrorDetails() != nil {
		return
	}
	details := ParseUpstreamError(data, eventType, secrets...)
	if details == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upstreamError == nil {
		s.upstreamError = details
	}
}

// UpstreamErrorMessage returns the bounded, masked diagnostic separately from
// StreamOutcome so it does not influence routing or billing classification.
func (s *StreamStatus) UpstreamErrorMessage() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upstreamError == nil {
		return ""
	}
	return s.upstreamError.Message
}

func (s *StreamStatus) UpstreamErrorDetails() *UpstreamErrorDetails {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upstreamError == nil {
		return nil
	}
	details := *s.upstreamError
	return &details
}

// CaptureUpstreamReadError runs at the read boundary, before a concurrent
// downstream cancellation can hide the cause. Locally induced cancellation and
// size enforcement are not evidence of a broken upstream connection.
func (s *StreamStatus) CaptureUpstreamReadError(err error) {
	if s == nil || err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, bufio.ErrTooLong) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upstreamReadErr == nil {
		s.upstreamReadErr = err
	}
}

// StreamDiagnostics keeps downstream termination separate from upstream facts.
type StreamDiagnostics struct {
	ClientError       error
	DrainTimedOut     bool
	UpstreamReadError error
	UpstreamEOF       bool
}

func (s *StreamStatus) DiagnosticsSnapshot() StreamDiagnostics {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StreamDiagnostics{
		ClientError: s.clientError, DrainTimedOut: s.drainTimedOut,
		UpstreamReadError: s.upstreamReadErr, UpstreamEOF: s.upstreamEOF,
	}
}

// MarkUpstreamEOF is called only for natural EOF, never for local body close.
func (s *StreamStatus) MarkUpstreamEOF() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstreamEOF = true
}

func (s *StreamStatus) MarkClientDisconnected(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.clientError == nil {
		s.clientError = err
	}
}

func (s *StreamStatus) MarkDrainTimedOut() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drainTimedOut = true
}

// RequireTerminal declares that the protocol always ends with an explicit
// terminal event, so a stream that ends without one was cut short.
func (s *StreamStatus) RequireTerminal() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expectsTerminal = true
}

// MarkCompleted, MarkIncomplete and MarkCancelled keep the first terminal seen;
// MarkFailed always wins because an error after completion is still a failure.
func (s *StreamStatus) MarkCompleted() {
	s.markTerminal(ResponseOutcomeCompleted, "")
}

func (s *StreamStatus) MarkIncomplete(reason string) {
	s.markTerminal(ResponseOutcomeIncomplete, reason)
}

func (s *StreamStatus) MarkCancelled() {
	s.markTerminal(ResponseOutcomeCancelled, "")
}

func (s *StreamStatus) markTerminal(outcome ResponseOutcome, incompleteReason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.response != ResponseOutcomeUnknown {
		return
	}
	s.response = outcome
	s.incompleteReason = incompleteReason
}

// MarkFailed records a protocol failure. Empty details never erase details
// recorded earlier, so a bare error envelope keeps the structured error.
func (s *StreamStatus) MarkFailed(code, errorType string, status int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.response = ResponseOutcomeFailed
	if code != "" {
		s.errorCode = code
	}
	if errorType != "" {
		s.errorType = errorType
	}
	if status != 0 {
		s.errorStatus = status
	}
}

func (s *StreamStatus) ResponseOutcome() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.response)
}

func (s *StreamStatus) ResponseFailed() bool {
	return s.ResponseOutcome() == string(ResponseOutcomeFailed)
}

func (s *StreamStatus) OutcomeSnapshot() StreamOutcome {
	if s == nil {
		return StreamOutcome{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return StreamOutcome{
		EndReason:        s.EndReason,
		HasErrors:        s.ErrorCount > 0,
		ExpectsTerminal:  s.expectsTerminal,
		Response:         s.response,
		ErrorCode:        s.errorCode,
		ErrorType:        s.errorType,
		ErrorStatus:      s.errorStatus,
		IncompleteReason: s.incompleteReason,
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	return s.EndReason == StreamEndReasonDone ||
		s.EndReason == StreamEndReasonEOF ||
		s.EndReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	s.mu.Lock()
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}
