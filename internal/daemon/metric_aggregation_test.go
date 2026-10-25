package daemon

import (
	"math"
	"sync/atomic"
	"testing"
	"time"
)

func snap(sec int, mem, net float64, total, idle int64) metricSnapshot {
	return metricSnapshot{at: time.Unix(int64(sec), 0), values: map[string]float64{"memory_used_percent.sh": mem, "network_receive_bytes_total.sh": net}, total: total, idle: idle, cpuOK: true}
}
func TestWindowAveragesRatesAndCPUTicks(t *testing.T) {
	a := newMetricAccumulator(snap(0, 10, 100, 0, 0))
	a.sample(snap(10, 20, 200, 100, 90))
	a.sample(snap(20, 40, 500, 300, 190))
	m := a.summaries["memory_used_percent.sh"]
	if m.sum/m.weight != 30 || m.min != 20 || m.max != 40 || m.count != 2 {
		t.Fatalf("memory: %+v", m)
	}
	n := a.summaries["network_receive_bytes_total.sh"]
	if n.sum/n.weight != 20 || n.min != 10 || n.max != 30 {
		t.Fatalf("network rate: %+v", n)
	}
	c := a.summaries["cpu_usage_percent.sh"]
	if math.Abs(c.sum/c.weight-100*110.0/300) > 1e-9 || c.min != 10 || c.max != 50 {
		t.Fatalf("cpu: %+v", c)
	}
}
func TestWindowDoesNotInventMissingOrResetIntervals(t *testing.T) {
	a := newMetricAccumulator(snap(0, 10, 100, 100, 50))
	a.sample(snap(10, 20, 10, 10, 5)) // counters reset
	if a.summaries["network_receive_bytes_total.sh"] != nil || a.summaries["cpu_usage_percent.sh"] != nil {
		t.Fatal("reset counted")
	}
	a.sample(snap(100, 30, 100, 100, 50)) // suspension
	if a.summaries["memory_used_percent.sh"].count != 1 {
		t.Fatal("gap counted")
	}
	a.sample(snap(110, 40, 200, 200, 100))
	if a.summaries["network_receive_bytes_total.sh"].count != 1 {
		t.Fatal("baseline not resumed")
	}
}
func TestWindowQueueIsBoundedAndOnlyAcknowledgesItsHead(t *testing.T) {
	w := &metricWindows{}
	for i := 0; i < 10; i++ {
		w.add(map[string]any{"ended_at": i})
	}
	if len(w.pending) != 6 || w.first()["ended_at"] != 4 {
		t.Fatal("unbounded queue")
	}
	w.ack(3)
	if w.first()["ended_at"] != 4 {
		t.Fatal("stale ack removed a window")
	}
	w.ack(4)
	if w.first()["ended_at"] != 5 {
		t.Fatal("ack not applied")
	}
}
func TestFullWindowHasThirtyIntervals(t *testing.T) {
	a := newMetricAccumulator(snap(0, 10, 0, 0, 0))
	for i := 1; i <= 30; i++ {
		a.sample(snap(i*10, float64(i), float64(i*100), int64(i*100), int64(i*90)))
	}
	s := a.summaries["memory_used_percent.sh"]
	if s.count != 30 || s.seconds != 300 || s.sum/s.weight != 15.5 {
		t.Fatalf("%+v", s)
	}
	p := a.finish()
	if p["started_at"] != "1970-01-01T00:00:00Z" || p["ended_at"] != "1970-01-01T00:05:00Z" {
		t.Fatalf("%+v", p)
	}
}

func TestRunRetriesWindowAfterConnectionFailure(t *testing.T) {
	f := newFakeServer(t)
	f.Join = func(c *fakeConn, topic string, p map[string]any) (string, map[string]any) {
		return "ok", map[string]any{"metric_windows": true}
	}
	var attempts atomic.Int32
	delivered := make(chan struct{}, 1)
	f.Handle = func(c *fakeConn, topic, event string, p map[string]any) (string, map[string]any) {
		if event == "metric_window" {
			if attempts.Add(1) == 1 {
				return "error", map[string]any{"reason": "retry"}
			}
			delivered <- struct{}{}
		}
		return "ok", map[string]any{}
	}
	w := &metricWindows{}
	w.add(map[string]any{"ended_at": "2026-01-01T00:05:00Z"})
	stop := runFor(t, RunOptions{URL: f.URL(), StatePath: pairedState(t), metricWindows: w, Collect: func() Inventory { return Inventory{} }, CollectStats: func() (Stats, bool) { return Stats{}, false }})
	select {
	case <-delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("window was not retried")
	}
	eventually(t, "window acknowledgment", func() bool { return w.first() == nil })
	stop()
	if w.first() != nil || attempts.Load() != 2 {
		t.Fatal("window was not acknowledged")
	}
}

func TestLinuxSnapshotSharesReadsAndDoesNotSpawnCommandsOrSleep(t *testing.T) {
	sys := linuxOps()
	sys.Now = func() time.Time { return time.Unix(0, 0) }
	original := sys.ReadFile
	reads := map[string]int{}
	sys.ReadFile = func(path string) ([]byte, error) { reads[path]++; return original(path) }
	sys.Run = func(string, ...string) (string, error) { t.Fatal("collector spawned a command"); return "", nil }
	sys.Sleep = func(time.Duration) { t.Fatal("collector blocked to measure CPU") }
	s := readMetricSnapshot(sys)
	if !s.cpuOK || s.values["memory_used_bytes.sh"] != 1024000000 {
		t.Fatalf("snapshot %+v", s)
	}
	for _, path := range []string{"/proc/stat", "/proc/meminfo", "/proc/net/dev", "/proc/diskstats"} {
		if reads[path] != 1 {
			t.Errorf("%s read %d times", path, reads[path])
		}
	}
}
