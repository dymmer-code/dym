package daemon

import (
	"context"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Windows are collected independently of the socket. At most six completed windows
// are kept during an outage; delivery is acknowledged and safe to retry.
type metricWindows struct {
	mu      sync.Mutex
	pending []map[string]any
}

func (w *metricWindows) add(p map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p)
	if len(w.pending) > 6 {
		w.pending = w.pending[1:]
	}
}
func (w *metricWindows) first() map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	return w.pending[0]
}
func (w *metricWindows) ack(end any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 && w.pending[0]["ended_at"] == end {
		w.pending = w.pending[1:]
	}
}

type metricReader struct {
	read    reading
	counter bool
}

var windowReaders = map[string]metricReader{
	"cpu_time_seconds_total.sh":       {cpuTime, true},
	"disk_io_duration_seconds.sh":     {diskIODuration, true},
	"disk_io_reads_total.sh":          {diskIOReads, true},
	"disk_io_writes_total.sh":         {diskIOWrites, true},
	"disk_used_bytes.sh":              {diskUsed, false},
	"memory_cached_bytes.sh":          {memoryCached, false},
	"memory_used_bytes.sh":            {memoryUsed, false},
	"memory_used_percent.sh":          {memoryUsedPercent, false},
	"network_active_connections.sh":   {networkConnections, false},
	"network_receive_bytes_total.sh":  {networkReceived, true},
	"network_transmit_bytes_total.sh": {networkTransmitted, true},
	"system_uptime_seconds.sh":        {uptime, false},
}

type metricSnapshot struct {
	at          time.Time
	values      map[string]float64
	total, idle int64
	cpuOK       bool
}

// Cache shared kernel reads for this tick only. No shell processes are needed on Linux.
func readMetricSnapshot(sys StatsSystem) metricSnapshot {
	original := sys.ReadFile
	files := map[string][]byte{}
	sys.ReadFile = func(path string) ([]byte, error) {
		if b, ok := files[path]; ok {
			return b, nil
		}
		b, e := original(path)
		if e == nil {
			files[path] = b
		}
		return b, e
	}
	run := sys.Run
	commands := map[string]string{}
	sys.Run = func(name string, args ...string) (string, error) {
		key := name + "\x00" + strings.Join(args, "\x00")
		if s, ok := commands[key]; ok {
			return s, nil
		}
		s, e := run(name, args...)
		if e == nil {
			commands[key] = s
		}
		return s, e
	}
	s := metricSnapshot{at: sys.Now(), values: map[string]float64{}}
	for name, r := range windowReaders {
		if text, ok := r.read(sys); ok {
			if v, e := strconv.ParseFloat(text, 64); e == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
				s.values[name] = v
			}
		}
	}
	if sys.GOOS == "linux" {
		s.total, s.idle, s.cpuOK = linuxCPU(sys)
	} else if sys.GOOS == "freebsd" {
		s.total, s.idle, s.cpuOK = freebsdCPU(sys)
	}
	return s
}

type metricSummary struct {
	sum, weight, min, max, seconds float64
	count                          int
}

func (s *metricSummary) add(v, weight, seconds float64) {
	if s.count == 0 {
		s.min = v
		s.max = v
	}
	s.min = math.Min(s.min, v)
	s.max = math.Max(s.max, v)
	s.sum += v * weight
	s.weight += weight
	s.seconds += seconds
	s.count++
}

type metricAccumulator struct {
	start     time.Time
	previous  metricSnapshot
	summaries map[string]*metricSummary
}

func newMetricAccumulator(first metricSnapshot) *metricAccumulator {
	return &metricAccumulator{start: first.at, previous: first, summaries: map[string]*metricSummary{}}
}
func (a *metricAccumulator) sample(s metricSnapshot) {
	seconds := s.at.Sub(a.previous.at).Seconds()
	// A suspended or overloaded collector must not pretend to have observed the gap.
	if seconds > 0 && seconds <= 20 {
		for name, v := range s.values {
			if windowReaders[name].counter {
				prev, ok := a.previous.values[name]
				if !ok || v < prev {
					continue
				}
				v = (v - prev) / seconds
			}
			a.add(name, v, seconds, seconds)
		}
		if s.cpuOK && a.previous.cpuOK {
			total := s.total - a.previous.total
			idle := s.idle - a.previous.idle
			if total > 0 && idle >= 0 && idle <= total {
				a.add("cpu_usage_percent.sh", 100*float64(total-idle)/float64(total), float64(total), seconds)
			}
		}
	}
	a.previous = s
}
func (a *metricAccumulator) add(name string, v, weight, seconds float64) {
	summary := a.summaries[name]
	if summary == nil {
		summary = &metricSummary{}
		a.summaries[name] = summary
	}
	summary.add(v, weight, seconds)
}
func (a *metricAccumulator) finish() map[string]any {
	metrics := map[string]any{}
	for name, s := range a.summaries {
		metrics[name] = map[string]any{"mean": s.sum / s.weight, "min": s.min, "max": s.max, "samples": s.count, "covered_seconds": s.seconds}
	}
	return map[string]any{"started_at": a.start.UTC().Format(time.RFC3339Nano), "ended_at": a.previous.at.UTC().Format(time.RFC3339Nano), "metrics": metrics}
}
func (w *metricWindows) collect(ctx context.Context, sys StatsSystem) {
	a := newMetricAccumulator(readMetricSnapshot(sys))
	nextWindow := time.Now().Add(5 * time.Minute)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case tick := <-ticker.C:
			s := readMetricSnapshot(sys)
			a.sample(s)
			if !tick.Before(nextWindow) {
				if s.at.Sub(a.start) <= time.Hour {
					w.add(a.finish())
				}
				a = newMetricAccumulator(s)
				for !nextWindow.After(tick) {
					nextWindow = nextWindow.Add(5 * time.Minute)
				}
			}
		}
	}
}
