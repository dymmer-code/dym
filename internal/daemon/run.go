package daemon

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"reflect"
	"runtime"
	"time"
)

// RunOptions says how the daemon runs.
type RunOptions struct {
	// URL is the websocket endpoint of Dymmer's daemon socket.
	URL string
	// StatePath is where the pairing is kept.
	StatePath string
	// Collect reads the inventory of the host; the default reads the machine.
	Collect func() Inventory
	// CollectStats takes a sample of how the host is doing; the default reads the machine. It says false when there
	// is nothing to send (a system that is not supported).
	CollectStats func() (Stats, bool)
	// Operations is what the operations use (the machine they read, the way to run a command, the configuration of the
	// owner); the default is the host itself.
	Operations *OpEnv
	// ConfigPath is the configuration of the owner (see Config); without it, the default of the user that runs.
	ConfigPath string
	// Logger receives what happens; the default discards it.
	Logger *slog.Logger
	// MinBackoff and MaxBackoff bound the wait between attempts to connect (1s and 60s by default).
	MinBackoff, MaxBackoff time.Duration
	// StableAfter is how long a connection has to last for the wait to start again from the minimum (60s).
	StableAfter time.Duration
	// StatsEvery is how often a sample is sent (5 minutes). Dymmer keeps one every quarter of an hour.
	StatsEvery time.Duration
	// InventoryEvery is how often the inventory is read again and sent if it changed (5 minutes).
	InventoryEvery time.Duration
	// PollPairing is how often it looks for the state while the host is not paired (5s).
	PollPairing time.Duration
}

func (o *RunOptions) defaults() {
	if o.Collect == nil {
		sys := HostSystem()
		o.Collect = func() Inventory { return Collect(sys) }
	}
	if o.CollectStats == nil {
		sys := HostStatsSystem(runtime.GOOS)
		o.CollectStats = func() (Stats, bool) { return CollectStats(sys) }
	}
	if o.StatsEvery == 0 {
		o.StatsEvery = 5 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.MinBackoff == 0 {
		o.MinBackoff = time.Second
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = time.Minute
	}
	if o.StableAfter == 0 {
		o.StableAfter = time.Minute
	}
	if o.InventoryEvery == 0 {
		o.InventoryEvery = 5 * time.Minute
	}
	if o.PollPairing == 0 {
		o.PollPairing = 5 * time.Second
	}
}

// Run keeps the host connected to Dymmer until ctx ends. While the host is not paired it only waits for the state
// that `dym auth daemon` leaves. A connection that fails or ends is retried with a wait that grows, so that a Dymmer
// that is down is not hammered by every host at once, and the wait starts again after a connection that held.
func Run(ctx context.Context, o RunOptions) error {
	o.defaults()
	wait := o.MinBackoff
	warnedUnpaired := false

	for ctx.Err() == nil {
		state, err := LoadState(o.StatePath)
		if err != nil {
			if errors.Is(err, ErrNotPaired) {
				if !warnedUnpaired {
					o.Logger.Info("this host is not paired yet: run `dym auth daemon`; waiting")
					warnedUnpaired = true
				}
			} else {
				o.Logger.Error("cannot read the pairing", "error", err)
			}
			sleep(ctx, o.PollPairing)
			continue
		}
		warnedUnpaired = false

		started := time.Now()
		err = connectOnce(ctx, o, state)
		if ctx.Err() != nil {
			return nil
		}
		o.Logger.Warn("disconnected from Dymmer", "error", err, "retry_in", wait.Round(time.Millisecond).String())

		if time.Since(started) >= o.StableAfter {
			wait = o.MinBackoff
		}
		sleep(ctx, jitter(wait))
		wait = min(wait*2, o.MaxBackoff)
	}
	return nil
}

// connectOnce connects, tells who the host is, and stays until the connection ends or ctx does.
func connectOnce(ctx context.Context, o RunOptions, state State) error {
	header := http.Header{}
	header.Set("X-Dym-Host", state.HostID)
	header.Set("X-Dym-Secret", state.Secret)

	conn, err := Dial(ctx, o.URL, header)
	if err != nil {
		var refused *DialError
		if errors.As(err, &refused) && refused.Status == http.StatusForbidden {
			o.Logger.Warn("Dymmer does not accept this host: it was deleted there or its secret was replaced; pair it again with `dym auth daemon`")
		}
		return err
	}
	defer conn.Close()

	sent := o.Collect()
	// It tells Dymmer which operations it answers, so that Dymmer asks only for those.
	hello := map[string]any{"inventory": sent.Payload(), "operations": OperationNames()}
	if _, err := conn.Join(ctx, "daemon:host", hello); err != nil {
		return err
	}
	o.Logger.Info("connected to Dymmer", "host_id", state.HostID)

	ticker := time.NewTicker(o.InventoryEvery)
	defer ticker.Stop()
	statsTicker := time.NewTicker(o.StatsEvery)
	defer statsTicker.Stop()

	sendStats := func() {
		stats, ok := o.CollectStats()
		if !ok {
			return
		}
		if _, err := conn.Push(ctx, "daemon:host", "stats", stats.Payload()); err != nil {
			o.Logger.Warn("cannot send the statistics", "error", err)
		}
	}
	sendStats()

	// Whatever the operations started ends with the connection.
	connCtx, endConn := context.WithCancel(ctx)
	defer endConn()
	ops := newOperationRunner(o, conn)

	for {
		select {
		case event := <-conn.Events():
			switch event.Name {
			case "exec":
				ops.handle(connCtx, event.Payload)
			case "stream_start":
				ops.startStream(connCtx, event.Payload)
			case "stream_stop":
				ops.stopStream(event.Payload)
			}
		case <-statsTicker.C:
			sendStats()
		case <-conn.Done():
			return conn.Err()
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			current := o.Collect()
			if reflect.DeepEqual(current, sent) {
				continue
			}
			if _, err := conn.Push(ctx, "daemon:host", "inventory", current.Payload()); err != nil {
				o.Logger.Warn("cannot send the inventory", "error", err)
				continue
			}
			sent = current
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// jitter spreads the hosts that lost Dymmer at the same moment: 80% to 120% of the wait.
func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}
