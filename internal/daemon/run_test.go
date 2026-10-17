package daemon

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runFor starts Run and returns a function that stops it and waits for it to end.
func runFor(t *testing.T, o RunOptions) (stop func()) {
	t.Helper()
	if o.MinBackoff == 0 {
		o.MinBackoff = 5 * time.Millisecond
	}
	if o.MaxBackoff == 0 {
		o.MaxBackoff = 20 * time.Millisecond
	}
	if o.PollPairing == 0 {
		o.PollPairing = 10 * time.Millisecond
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, o) }()

	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Run returned %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Error("Run did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func pairedState(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "daemon.json")
	if err := SaveState(path, State{HostID: "host-1", Secret: "dymd_secret"}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunConnectsWithTheSecretInHeadersAndTellsWhoTheHostIs(t *testing.T) {
	f := newFakeServer(t)
	runFor(t, RunOptions{URL: f.URL(), StatePath: pairedState(t), Collect: func() Inventory { return Inventory{Hostname: "rowan", CPUCores: 4} }})

	eventually(t, "the join", func() bool { return len(f.Joins()) == 1 })

	h := f.Headers()[0]
	if h.Get("X-Dym-Host") != "host-1" || h.Get("X-Dym-Secret") != "dymd_secret" {
		t.Errorf("headers = %v", h)
	}
	if q := f.Queries()[0]; q != "vsn=2.0.0" {
		t.Errorf("the secret must not be in the URL: %q", q)
	}
	join := f.Joins()[0]
	inv, _ := join.Payload["inventory"].(map[string]any)
	if join.Topic != "daemon:host" || inv["hostname"] != "rowan" {
		t.Errorf("join = %+v", join)
	}
}

func TestRunWaitsWhileTheHostIsNotPairedAndConnectsOnceItIs(t *testing.T) {
	f := newFakeServer(t)
	path := filepath.Join(t.TempDir(), "daemon.json")
	runFor(t, RunOptions{URL: f.URL(), StatePath: path})

	time.Sleep(60 * time.Millisecond)
	if len(f.Headers()) != 0 {
		t.Fatal("it must not connect before it is paired")
	}

	if err := SaveState(path, State{HostID: "host-2", Secret: "dymd_s"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the connection after pairing", func() bool { return len(f.Headers()) == 1 })
	if f.Headers()[0].Get("X-Dym-Host") != "host-2" {
		t.Error("it must connect as the host it was paired as")
	}
}

func TestRunReconnectsWhenTheConnectionDrops(t *testing.T) {
	f := newFakeServer(t)
	var drops atomic.Int32
	f.Join = func(c *fakeConn, _ string, _ map[string]any) (string, map[string]any) {
		if drops.Add(1) == 1 {
			go func() {
				time.Sleep(20 * time.Millisecond)
				c.Drop()
			}()
		}
		return "ok", map[string]any{}
	}
	runFor(t, RunOptions{URL: f.URL(), StatePath: pairedState(t)})

	eventually(t, "a second connection", func() bool { return len(f.Joins()) >= 2 })
}

func TestRunKeepsTryingWhileDymmerRefusesAndConnectsWhenItAccepts(t *testing.T) {
	f := newFakeServer(t)
	var attempts atomic.Int32
	f.Handshake = func(*http.Request) int {
		if attempts.Add(1) <= 3 {
			return http.StatusForbidden
		}
		return 0
	}
	runFor(t, RunOptions{URL: f.URL(), StatePath: pairedState(t)})

	eventually(t, "the first accepted connection", func() bool { return len(f.Joins()) == 1 })
	if attempts.Load() < 4 {
		t.Errorf("attempts = %d", attempts.Load())
	}
}

func TestRunSendsTheInventoryAgainOnlyWhenItChanged(t *testing.T) {
	f := newFakeServer(t)
	var pushes atomic.Int32
	var last atomic.Value
	f.Handle = func(_ *fakeConn, _, event string, payload map[string]any) (string, map[string]any) {
		if event == "inventory" {
			pushes.Add(1)
			last.Store(payload)
		}
		return "ok", map[string]any{}
	}

	var memory atomic.Int64
	memory.Store(100)
	runFor(t, RunOptions{
		URL: f.URL(), StatePath: pairedState(t), InventoryEvery: 15 * time.Millisecond,
		Collect: func() Inventory { return Inventory{Hostname: "rowan", Memory: memory.Load()} },
	})

	eventually(t, "the join", func() bool { return len(f.Joins()) == 1 })
	time.Sleep(80 * time.Millisecond)
	if pushes.Load() != 0 {
		t.Fatalf("nothing changed, yet it sent %d inventories", pushes.Load())
	}

	memory.Store(200)
	eventually(t, "the new inventory", func() bool { return pushes.Load() == 1 })
	time.Sleep(60 * time.Millisecond)
	if pushes.Load() != 1 {
		t.Errorf("it must send a change once, sent %d", pushes.Load())
	}
	if got, _ := last.Load().(map[string]any); got["memory"] != float64(200) {
		t.Errorf("sent %v", got)
	}
}

func TestRunStopsPromptlyWhenTheContextEnds(t *testing.T) {
	f := newFakeServer(t)
	stop := runFor(t, RunOptions{URL: f.URL(), StatePath: pairedState(t)})
	eventually(t, "the join", func() bool { return len(f.Joins()) == 1 })

	started := time.Now()
	stop()

	if time.Since(started) > time.Second {
		t.Error("Run took too long to stop")
	}
}

func TestJitterStaysWithinTwentyPercent(t *testing.T) {
	for i := 0; i < 200; i++ {
		got := jitter(time.Second)
		if got < 800*time.Millisecond || got > 1200*time.Millisecond {
			t.Fatalf("jitter = %v", got)
		}
	}
}

func TestRunSendsStatisticsWhenItJoinsAndThenEveryInterval(t *testing.T) {
	f := newFakeServer(t)
	var samples atomic.Int32
	var last atomic.Value
	f.Handle = func(_ *fakeConn, _, event string, payload map[string]any) (string, map[string]any) {
		if event == "stats" {
			samples.Add(1)
			last.Store(payload)
		}
		return "ok", map[string]any{}
	}

	var load atomic.Int64
	load.Store(1)
	runFor(t, RunOptions{
		URL: f.URL(), StatePath: pairedState(t), StatsEvery: 20 * time.Millisecond,
		Collect: func() Inventory { return Inventory{Hostname: "rowan"} },
		CollectStats: func() (Stats, bool) {
			return Stats{CPULoad: float64(load.Load()), MemoryUsage: 42, Uptime: 7}, true
		},
	})

	eventually(t, "the first sample", func() bool { return samples.Load() >= 1 })
	eventually(t, "more samples", func() bool { return samples.Load() >= 3 })
	got, _ := last.Load().(map[string]any)
	if got["memory_usage"] != float64(42) || got["uptime"] != float64(7) {
		t.Errorf("sent %v", got)
	}
}

func TestRunSendsNoStatisticsWhenThereIsNothingToSend(t *testing.T) {
	f := newFakeServer(t)
	var samples atomic.Int32
	f.Handle = func(_ *fakeConn, _, event string, _ map[string]any) (string, map[string]any) {
		if event == "stats" {
			samples.Add(1)
		}
		return "ok", map[string]any{}
	}
	runFor(t, RunOptions{
		URL: f.URL(), StatePath: pairedState(t), StatsEvery: 10 * time.Millisecond,
		Collect:      func() Inventory { return Inventory{Hostname: "x"} },
		CollectStats: func() (Stats, bool) { return Stats{}, false },
	})

	eventually(t, "the join", func() bool { return len(f.Joins()) == 1 })
	time.Sleep(80 * time.Millisecond)
	if samples.Load() != 0 {
		t.Errorf("sent %d samples of a system that gives none", samples.Load())
	}
}

// orders collects the "exec_result" messages that the client sends back.
type orders struct {
	mu      sync.Mutex
	results []map[string]any
}

func (o *orders) add(payload map[string]any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.results = append(o.results, payload)
}

func (o *orders) byID(id string) map[string]any {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, r := range o.results {
		if r["id"] == id {
			return r
		}
	}
	return nil
}

func runWithOperations(t *testing.T, sys StatsSystem) (*fakeServer, *orders) {
	t.Helper()
	f := newFakeServer(t)
	got := &orders{}
	f.Handle = func(_ *fakeConn, _, event string, payload map[string]any) (string, map[string]any) {
		if event == "exec_result" {
			got.add(payload)
		}
		return "ok", map[string]any{}
	}
	runFor(t, RunOptions{
		URL: f.URL(), StatePath: pairedState(t), Operations: &sys,
		Collect:      func() Inventory { return Inventory{Hostname: "rowan"} },
		CollectStats: func() (Stats, bool) { return Stats{}, false },
	})
	eventually(t, "the join", func() bool { return len(f.Joins()) == 1 })
	return f, got
}

func TestRunTellsDymmerWhichOperationsItAnswers(t *testing.T) {
	f, _ := runWithOperations(t, linuxOps())

	names, _ := f.Joins()[0].Payload["operations"].([]any)
	if len(names) != len(OperationNames()) || names[0] != OperationNames()[0] {
		t.Errorf("the join said %v", names)
	}
}

func TestRunAnswersAnOrderWithTheResultOfThatOperation(t *testing.T) {
	f, got := runWithOperations(t, linuxOps())

	f.Last().Push("daemon:host", "exec", map[string]any{"id": "o1", "name": "memory_used_bytes.sh", "args": []any{}})

	eventually(t, "the result", func() bool { return got.byID("o1") != nil })
	r := got.byID("o1")
	if r["status"] != "OK" || r["message"] != "1024000000" {
		t.Errorf("answered %v", r)
	}
}

func TestRunRefusesWhatIsNotAnOperationOrIsMalformed(t *testing.T) {
	ran := false
	sys := linuxOps()
	sys.Run = func(string, ...string) (string, error) { ran = true; return "", nil }
	f, got := runWithOperations(t, sys)

	orders := map[string]map[string]any{
		"unknown":   {"id": "u", "name": "rm -rf /", "args": []any{}},
		"arguments": {"id": "a", "name": "cpu_usage_percent.sh", "args": []any{"x"}},
		"badargs":   {"id": "b", "name": "cpu_usage_percent.sh", "args": "x"},
		"nonstring": {"id": "n", "name": "cpu_usage_percent.sh", "args": []any{5}},
		"toomany":   {"id": "m", "name": "cpu_usage_percent.sh", "args": []any{"1", "2", "3", "4", "5", "6", "7", "8", "9"}},
		"toolong":   {"id": "l", "name": "cpu_usage_percent.sh", "args": []any{strings.Repeat("a", 600)}},
	}
	for _, order := range orders {
		f.Last().Push("daemon:host", "exec", order)
	}
	for _, id := range []string{"u", "a", "b", "n", "m", "l"} {
		id := id
		eventually(t, "the refusal of "+id, func() bool { return got.byID(id) != nil })
		if got.byID(id)["status"] != "FAIL" {
			t.Errorf("%s was not refused: %v", id, got.byID(id))
		}
	}
	if ran {
		t.Error("something was run for an order that was not valid")
	}
}

func TestRunIgnoresAnOrderWithoutAnId(t *testing.T) {
	f, got := runWithOperations(t, linuxOps())

	f.Last().Push("daemon:host", "exec", map[string]any{"name": "memory_used_bytes.sh"})
	f.Last().Push("daemon:host", "exec", map[string]any{"id": "after", "name": "memory_used_bytes.sh"})

	eventually(t, "the order that has an id", func() bool { return got.byID("after") != nil })
	got.mu.Lock()
	defer got.mu.Unlock()
	if len(got.results) != 1 {
		t.Errorf("answered %d orders, want only the one with an id", len(got.results))
	}
}

func TestRunIsBusyBeyondTheLimitAndAnswersTheRest(t *testing.T) {
	release := make(chan struct{})
	sys := linuxOps()
	sys.ReadFile = func(path string) ([]byte, error) {
		<-release // every operation waits here, so the limit is reached
		return []byte(procMeminfo), nil
	}
	f, got := runWithOperations(t, sys)

	for i := 0; i < maxRunning+2; i++ {
		f.Last().Push("daemon:host", "exec", map[string]any{"id": "b" + string(rune('a'+i)), "name": "memory_used_bytes.sh"})
	}
	busy := 0
	eventually(t, "the refusals for being busy", func() bool {
		got.mu.Lock()
		defer got.mu.Unlock()
		busy = 0
		for _, r := range got.results {
			if r["message"] == "the host is busy" {
				busy++
			}
		}
		return busy == 2
	})
	close(release)
	eventually(t, "the ones that were running", func() bool {
		got.mu.Lock()
		defer got.mu.Unlock()
		return len(got.results) == maxRunning+2
	})
}
