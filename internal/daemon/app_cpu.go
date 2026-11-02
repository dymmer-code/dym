package daemon

import (
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

// CPU time is read as a cumulative counter in nanoseconds: the consumer takes the
// difference between two observations of the same instance and method. A counter
// that goes down means the group restarted or lost processes, never negative use.
const cpuUsageMetric = "cpu_usage_ns"

// parseCPUTime reads the TIME column of ps: [dd-][hh:]mm:ss[.ff].
func parseCPUTime(s string) (int64, error) {
	bad := memError("error", "invalid_counter")
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return 0, bad
	}
	var days int64
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.ParseInt(d, 10, 64)
		if err != nil || n < 0 || n > 36500 {
			return 0, bad
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, bad
	}
	var secs float64 = float64(days) * 86400
	mult := 1.0
	for i := len(parts) - 1; i >= 0; i-- {
		v, err := strconv.ParseFloat(parts[i], 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, bad
		}
		secs += v * mult
		mult *= 60
	}
	if secs > 1e9 {
		return 0, bad
	}
	return int64(math.Round(secs * 1e9)), nil
}

// hostCPUMillis is the capacity of the machine in millicores (logical CPUs times 1000).
func hostCPUMillis(env OpEnv, c *memoryCollector) (int64, bool) {
	if env.GOOS == "freebsd" {
		if c == nil {
			return 0, false
		}
		out, err := c.run("sysctl", "-n", "hw.ncpu")
		if err != nil {
			return 0, false
		}
		n, e := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		if e != nil || n < 1 || n > 4096 {
			return 0, false
		}
		return n * 1000, true
	}
	if data, err := env.ReadFile("/sys/devices/system/cpu/online"); err == nil {
		n := 0
		for _, r := range strings.Split(strings.TrimSpace(string(data)), ",") {
			lo, hi, isRange := strings.Cut(r, "-")
			a, e1 := strconv.Atoi(lo)
			if e1 != nil || a < 0 {
				n = 0
				break
			}
			if !isRange {
				n++
				continue
			}
			b, e2 := strconv.Atoi(hi)
			if e2 != nil || b < a {
				n = 0
				break
			}
			n += b - a + 1
		}
		if n >= 1 && n <= 4096 {
			return int64(n) * 1000, true
		}
	}
	if data, err := env.ReadFile("/proc/cpuinfo"); err == nil {
		n := 0
		for _, l := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(l, "processor") {
				n++
			}
		}
		if n >= 1 && n <= 4096 {
			return int64(n) * 1000, true
		}
	}
	return 0, false
}

// hostMemoryBytes is the physical memory of the machine.
func hostMemoryBytes(env OpEnv, c *memoryCollector) (int64, bool) {
	if env.GOOS == "freebsd" {
		if c == nil {
			return 0, false
		}
		out, err := c.run("sysctl", "-n", "hw.physmem")
		if err != nil {
			return 0, false
		}
		n, e := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		if e != nil || n < 1 {
			return 0, false
		}
		return n, true
	}
	data, err := env.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for _, l := range strings.Split(string(data), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "MemTotal:" {
			n, e := strconv.ParseInt(f[1], 10, 64)
			if e != nil || n < 1 || n > math.MaxInt64/1024 {
				return 0, false
			}
			return n * 1024, true
		}
	}
	return 0, false
}

func withCapacity(r MemoryReading, capacity int64, source string) MemoryReading {
	r.Capacity = &capacity
	r.CapacitySource = source
	return r
}

// cgroupCPUReading reads the CPU time of the group, or nothing when the host does not offer it:
// memory is still reported without it.
func cgroupCPUReading(env OpEnv, pid int, kind, v2dir string) *MemoryReading {
	var usage, quota, period int64 = -1, -1, 0
	method := "cgroup_v2"
	if kind == "cgroup2" {
		stat, err := env.ReadFile(filepath.Join(v2dir, "cpu.stat"))
		if err != nil {
			return nil
		}
		for _, l := range strings.Split(string(stat), "\n") {
			f := strings.Fields(l)
			if len(f) == 2 && f[0] == "usage_usec" {
				n, e := strconv.ParseInt(f[1], 10, 64)
				if e != nil || n < 0 || n > math.MaxInt64/1000 {
					return nil
				}
				usage = n * 1000
			}
		}
		if raw, err := env.ReadFile(filepath.Join(v2dir, "cpu.max")); err == nil {
			f := strings.Fields(string(raw))
			if len(f) == 2 && f[0] != "max" {
				q, e1 := strconv.ParseInt(f[0], 10, 64)
				p, e2 := strconv.ParseInt(f[1], 10, 64)
				if e1 == nil && e2 == nil && q > 0 && p > 0 {
					quota, period = q, p
				}
			}
		}
	} else {
		method = "cgroup_v1"
		dir, withCPU := cgroupV1Dir(env, pid, "cpuacct")
		if dir == "" {
			return nil
		}
		raw, err := env.ReadFile(filepath.Join(dir, "cpuacct.usage"))
		if err != nil {
			return nil
		}
		n, e := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if e != nil || n < 0 {
			return nil
		}
		usage = n
		if withCPU {
			q, e1 := readInt(env, filepath.Join(dir, "cpu.cfs_quota_us"))
			p, e2 := readInt(env, filepath.Join(dir, "cpu.cfs_period_us"))
			if e1 == nil && e2 == nil && q > 0 && p > 0 {
				quota, period = q, p
			}
		}
	}
	if usage < 0 {
		return nil
	}
	r := MemoryReading{Metric: cpuUsageMetric, Method: method, Value: usage, Accuracy: "kernel_accounted"}
	if quota > 0 && period > 0 {
		r = withCapacity(r, quota*1000/period, "limit")
	} else if cores, ok := hostCPUMillis(env, nil); ok {
		r = withCapacity(r, cores, "host")
	}
	return &r
}

func readInt(env OpEnv, path string) (int64, error) {
	raw, err := env.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
}

// cgroupV1Dir finds the directory of the group of pid in the hierarchy that has the controller,
// and whether the same hierarchy also has the cpu controller (for the quota).
func cgroupV1Dir(env OpEnv, pid int, controller string) (string, bool) {
	data, err := env.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", false
	}
	group := ""
	for _, l := range strings.Split(string(data), "\n") {
		p := strings.SplitN(l, ":", 3)
		if len(p) == 3 && includes(strings.Split(p[1], ","), controller) {
			group = p[2]
		}
	}
	if !cleanAbsolute(group) || group == "/" {
		return "", false
	}
	mounts, err := env.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", false
	}
	for _, l := range strings.Split(string(mounts), "\n") {
		a, b, ok := strings.Cut(l, " - ")
		if !ok {
			continue
		}
		left, right := strings.Fields(a), strings.Fields(b)
		if len(left) < 6 || len(right) < 3 || right[0] != "cgroup" {
			continue
		}
		opts := strings.Split(right[2], ",")
		if !includes(opts, controller) {
			continue
		}
		root, mount := unescapeMount(left[3]), unescapeMount(left[4])
		if !cleanAbsolute(root) || !cleanAbsolute(mount) {
			continue
		}
		if group != root && !strings.HasPrefix(group, strings.TrimSuffix(root, "/")+"/") {
			continue
		}
		return filepath.Join(mount, strings.TrimPrefix(group, root)), includes(opts, "cpu")
	}
	return "", false
}

// processUsageReadings are the readings of a standalone process tree: resident memory and CPU time.
func (c memoryCollector) processUsageReadings(rss, cpuNS int64, cpuKnown bool) []MemoryReading {
	mem := MemoryReading{Metric: "memory_rss_bytes", Method: "process_rss", Value: rss, Accuracy: "shared_pages_may_repeat"}
	if total, ok := hostMemoryBytes(c.env, &c); ok {
		mem = withCapacity(mem, total, "host")
	}
	out := []MemoryReading{mem}
	if cpuKnown {
		cpu := MemoryReading{Metric: cpuUsageMetric, Method: "process_cputime", Value: cpuNS, Accuracy: "live_processes_only"}
		if cores, ok := hostCPUMillis(c.env, &c); ok {
			cpu = withCapacity(cpu, cores, "host")
		}
		out = append(out, cpu)
	}
	return out
}
