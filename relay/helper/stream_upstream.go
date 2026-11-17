package helper

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

var errStreamDrainTimeout = errors.New("upstream stream drain deadline exceeded")

// StreamDrainTimeout is a total budget, independent of upstream heartbeats.
// The normal stream idle timeout can shorten, but never extend, this budget.
// Zero disables draining; negative values fall back to the three-second default.
func StreamDrainTimeout() time.Duration {
	seconds := constant.StreamDrainTimeout
	if seconds < 0 {
		seconds = 3
	}
	if constant.StreamingTimeout > 0 {
		seconds = min(seconds, constant.StreamingTimeout)
	}
	return time.Duration(seconds) * time.Second
}

// NewStreamUpstreamContext retains request values while allowing the upstream
// to finish reporting errors and usage after the client leaves. Call release on
// request failure or response-body close, including for HTTP error responses.
func NewStreamUpstreamContext(client, upstream context.Context) (context.Context, context.CancelFunc) {
	if client.Err() != nil {
		return client, func() {}
	}
	parent := context.WithoutCancel(upstream)
	releaseDeadline := func() {}
	// Provider/task deadlines remain authoritative even while draining.
	if deadline, ok := upstream.Deadline(); ok {
		parent, releaseDeadline = context.WithDeadline(parent, deadline)
	}
	ctx, cancel := context.WithCancelCause(parent)
	timeout := StreamDrainTimeout()
	var mu sync.Mutex
	var timer *time.Timer
	closed := false
	stop := context.AfterFunc(client, func() {
		if errors.Is(context.Cause(client), relaycommon.ErrStreamAborted) {
			cancel(relaycommon.ErrStreamAborted)
			return
		}
		if timeout == 0 {
			cancel(context.Cause(client))
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			timer = time.AfterFunc(timeout, func() { cancel(errStreamDrainTimeout) })
		}
	})
	return ctx, func() {
		stop()
		mu.Lock()
		closed = true
		if timer != nil {
			timer.Stop()
		}
		mu.Unlock()
		cancel(context.Canceled)
		releaseDeadline()
	}
}
