package daemon

import (
	"context"
	"log/slog"
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
}

func newOperationRunner(o RunOptions, conn *Conn) *operationRunner {
	env := HostOpEnv(currentGOOS, o.ConfigPath)
	if o.Operations != nil {
		env = *o.Operations
	}
	return &operationRunner{env: env, conn: conn, logger: o.Logger, running: make(chan struct{}, maxRunning)}
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
