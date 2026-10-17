package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	// maxRunning bounds how many operations run at once: Dymmer asks a few at a time, and a host is not to be loaded by
	// whoever asks too much.
	maxRunning = 4
	// operationTimeout bounds one operation. Dymmer gives up earlier, so this only stops what nobody waits for.
	operationTimeout = 20 * time.Second
	// maxArgs and maxArgLength bound what an order may carry, before any operation looks at it.
	maxArgs      = 8
	maxArgLength = 512
)

// operationRunner answers the "exec" orders that Dymmer pushes: it runs the operation that was named, if it is one of
// ours, and sends back what it printed. Nothing else is ever run.
type operationRunner struct {
	env     OpEnv
	conn    *Conn
	logger  *slog.Logger
	running chan struct{}

	mu      sync.Mutex
	streams map[string]context.CancelFunc
}

// maxStreams bounds how many streams are open at once.
const maxStreams = 2

func newOperationRunner(o RunOptions, conn *Conn) *operationRunner {
	env := HostOpEnv(currentGOOS, o.ConfigPath)
	if o.Operations != nil {
		env = *o.Operations
	}
	return &operationRunner{env: env, conn: conn, logger: o.Logger, running: make(chan struct{}, maxRunning), streams: map[string]context.CancelFunc{}}
}

// handle takes an "exec" order: {"id": "...", "name": "cpu_usage_percent.sh", "args": []}. The answer goes back as an
// "exec_result" with the same id; an order that cannot be read, or that comes when the host is busy, is answered with a
// failure and not dropped, so that Dymmer does not wait for it.
func (r *operationRunner) handle(ctx context.Context, order map[string]any) {
	id, _ := order["id"].(string)
	name, _ := order["name"].(string)
	if id == "" {
		r.logger.Warn("an order without an id was ignored")
		return
	}

	args, valid := stringArgs(order["args"])
	if !valid {
		r.answer(ctx, id, fail("the arguments are not valid"))
		return
	}

	select {
	case r.running <- struct{}{}:
	default:
		r.answer(ctx, id, fail("the host is busy"))
		return
	}

	go func() {
		defer func() { <-r.running }()
		opCtx, cancel := context.WithTimeout(ctx, operationTimeout)
		defer cancel()

		done := make(chan Result, 1)
		go func() { done <- RunOperation(r.env, name, args) }()

		select {
		case result := <-done:
			r.answer(ctx, id, result)
		case <-opCtx.Done():
			r.answer(ctx, id, fail("the operation took too long"))
		}
	}()
}

func (r *operationRunner) answer(ctx context.Context, id string, result Result) {
	payload := result.Payload()
	payload["id"] = id
	if _, err := r.conn.Push(ctx, "daemon:host", "exec_result", payload); err != nil {
		r.logger.Warn("cannot send the result of an operation", "error", err)
	}
}

// stringArgs reads the arguments of an order: a list of at most maxArgs texts of at most maxArgLength characters.
func stringArgs(raw any) ([]string, bool) {
	if raw == nil {
		return nil, true
	}
	list, isList := raw.([]any)
	if !isList || len(list) > maxArgs {
		return nil, false
	}
	args := make([]string, 0, len(list))
	for _, item := range list {
		text, isText := item.(string)
		if !isText || len(text) > maxArgLength {
			return nil, false
		}
		args = append(args, text)
	}
	return args, true
}

// startStream takes a "stream_start" order: {"id": "...", "name": "log_follow.sh", "args": [...]}. The lines go back
// as "stream_data" messages and the end as one "stream_end" with the result, which says why it ended. It ends when
// Dymmer sends "stream_stop", when the connection ends, or when an hour has passed.
func (r *operationRunner) startStream(ctx context.Context, order map[string]any) {
	id, _ := order["id"].(string)
	name, _ := order["name"].(string)
	if id == "" {
		r.logger.Warn("a stream order without an id was ignored")
		return
	}
	stream, known := Streams()[name]
	args, valid := stringArgs(order["args"])
	switch {
	case !known:
		r.end(ctx, id, fail("unknown operation"))
		return
	case !valid:
		r.end(ctx, id, fail("the arguments are not valid"))
		return
	}

	r.mu.Lock()
	if len(r.streams) >= maxStreams {
		r.mu.Unlock()
		r.end(ctx, id, fail("the host has too many streams open"))
		return
	}
	streamCtx, cancel := context.WithTimeout(ctx, maxStreamTime)
	r.streams[id] = cancel
	r.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.streams, id)
			r.mu.Unlock()
		}()
		result := stream(streamCtx, r.env, args, func(data string) {
			if _, err := r.conn.Push(ctx, "daemon:host", "stream_data", map[string]any{"id": id, "data": data}); err != nil {
				cancel() // nobody receives it
			}
		})
		r.end(ctx, id, result)
	}()
}

// stopStream takes a "stream_stop" order.
func (r *operationRunner) stopStream(order map[string]any) {
	id, _ := order["id"].(string)
	r.mu.Lock()
	cancel := r.streams[id]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *operationRunner) end(ctx context.Context, id string, result Result) {
	payload := result.Payload()
	payload["id"] = id
	if _, err := r.conn.Push(ctx, "daemon:host", "stream_end", payload); err != nil {
		r.logger.Warn("cannot send the end of a stream", "error", err)
	}
}
