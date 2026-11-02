package daemon

import (
	"context"
	"strings"
	"testing"
)

func readingFor(readings []MemoryReading, metric string) *MemoryReading {
	for i := range readings {
		if readings[i].Metric == metric {
			return &readings[i]
		}
	}
	return nil
}

func cpuCgroupFiles(version string) map[string]string {
	files := map[string]string{
		"/proc/42/cgroup":                         "0::/apps/demo\n",
		"/proc/self/mountinfo":                    "25 1 0:23 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n",
		"/sys/fs/cgroup/apps/demo/memory.current": "16236544",
		"/sys/fs/cgroup/apps/demo/memory.max":     "max",
		"/sys/fs/cgroup/apps/demo/memory.stat":    "file 10125312\n",
		"/sys/fs/cgroup/apps/demo/cpu.stat":       "usage_usec 2500000\nuser_usec 2000000\nsystem_usec 500000\n",
		"/sys/fs/cgroup/apps/demo/cpu.max":        "max 100000\n",
		"/sys/devices/system/cpu/online":          "0-3\n",
		"/proc/meminfo":                           "MemTotal:        8000000 kB\nMemFree: 1 kB\n",
	}
	if version == "cgroup" {
		files["/proc/42/cgroup"] = "4:cpu,cpuacct:/apps/demo\n2:memory:/apps/demo\n"
		files["/proc/self/mountinfo"] = "25 1 0:23 / /sys/fs/cgroup/memory rw - cgroup cgroup rw,memory\n26 1 0:24 / /sys/fs/cgroup/cpu,cpuacct rw - cgroup cgroup rw,cpu,cpuacct\n"
		delete(files, "/sys/fs/cgroup/apps/demo/memory.current")
		delete(files, "/sys/fs/cgroup/apps/demo/memory.max")
		delete(files, "/sys/fs/cgroup/apps/demo/memory.stat")
		delete(files, "/sys/fs/cgroup/apps/demo/cpu.stat")
		delete(files, "/sys/fs/cgroup/apps/demo/cpu.max")
		files["/sys/fs/cgroup/memory/apps/demo/memory.usage_in_bytes"] = "375103488"
		files["/sys/fs/cgroup/memory/apps/demo/memory.limit_in_bytes"] = "4294967296"
		files["/sys/fs/cgroup/memory/apps/demo/memory.stat"] = "total_cache 213651456\n"
		files["/sys/fs/cgroup/cpu,cpuacct/apps/demo/cpuacct.usage"] = "7000000000\n"
		files["/sys/fs/cgroup/cpu,cpuacct/apps/demo/cpu.cfs_quota_us"] = "200000\n"
		files["/sys/fs/cgroup/cpu,cpuacct/apps/demo/cpu.cfs_period_us"] = "100000\n"
	}
	return files
}

func TestCgroupV2CPUUsesHostCoresWithoutQuota(t *testing.T) {
	readings, _, err := readAppCgroup(memoryEnv(cpuCgroupFiles("cgroup2")), 42)
	if err != nil {
		t.Fatal(err)
	}
	cpu := readingFor(readings, "cpu_usage_ns")
	if cpu == nil || cpu.Value != 2500000*1000 || cpu.Method != "cgroup_v2" {
		t.Fatalf("%+v", readings)
	}
	if cpu.Capacity == nil || *cpu.Capacity != 4000 || cpu.CapacitySource != "host" {
		t.Fatalf("cores of the host in millicores expected: %+v", cpu)
	}
	mem := readingFor(readings, "memory_accounted_bytes")
	if mem.Capacity == nil || *mem.Capacity != 8000000*1024 || mem.CapacitySource != "host" {
		t.Fatalf("host memory expected when the group has no limit: %+v", mem)
	}
}

func TestCgroupV2CPUQuotaIsTheCapacity(t *testing.T) {
	files := cpuCgroupFiles("cgroup2")
	files["/sys/fs/cgroup/apps/demo/cpu.max"] = "150000 100000\n"
	files["/sys/fs/cgroup/apps/demo/memory.max"] = "2147483648"
	readings, _, err := readAppCgroup(memoryEnv(files), 42)
	if err != nil {
		t.Fatal(err)
	}
	cpu := readingFor(readings, "cpu_usage_ns")
	if cpu.Capacity == nil || *cpu.Capacity != 1500 || cpu.CapacitySource != "limit" {
		t.Fatalf("%+v", cpu)
	}
	mem := readingFor(readings, "memory_accounted_bytes")
	if *mem.Capacity != 2147483648 || mem.CapacitySource != "limit" {
		t.Fatalf("%+v", mem)
	}
}

func TestCgroupV1CPUFromCpuacct(t *testing.T) {
	readings, _, err := readAppCgroup(memoryEnv(cpuCgroupFiles("cgroup")), 42)
	if err != nil {
		t.Fatal(err)
	}
	cpu := readingFor(readings, "cpu_usage_ns")
	if cpu == nil || cpu.Value != 7000000000 || cpu.Method != "cgroup_v1" {
		t.Fatalf("%+v", readings)
	}
	if cpu.Capacity == nil || *cpu.Capacity != 2000 || cpu.CapacitySource != "limit" {
		t.Fatalf("%+v", cpu)
	}
}

func TestMissingCPUCounterOmitsOnlyTheCPUReading(t *testing.T) {
	files := cpuCgroupFiles("cgroup2")
	delete(files, "/sys/fs/cgroup/apps/demo/cpu.stat")
	readings, _, err := readAppCgroup(memoryEnv(files), 42)
	if err != nil {
		t.Fatal(err)
	}
	if readingFor(readings, "cpu_usage_ns") != nil || readingFor(readings, "memory_accounted_bytes") == nil {
		t.Fatalf("%+v", readings)
	}
}

func TestCPUTimeParsing(t *testing.T) {
	for in, want := range map[string]int64{
		"0:00.00":    0,
		"0:15.25":    15250000000,
		"01:02":      62000000000,
		"1:01:02":    3662000000000,
		"2-01:00:00": (2*86400 + 3600) * 1000000000,
	} {
		got, err := parseCPUTime(in)
		if err != nil || got != want {
			t.Fatalf("%q: %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "1:2:3:4", "-1:00", "1:xx"} {
		if _, err := parseCPUTime(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestFreeBSDProcessCPUSumsTheTreeAndUsesHostCapacity(t *testing.T) {
	uid := uint32(1002)
	e := memoryEnv(nil)
	e.GOOS = "freebsd"
	e.Config = func() (Config, error) {
		return Config{Memory: []MemoryTarget{{Manager: "service", Service: "dnsauth", UID: &uid, Executable: "dnsauth"}}}, nil
	}
	e.Exec = func(_ context.Context, s ExecSpec) ExecResult {
		switch strings.Join(s.Argv, " ") {
		case "sysctl -n kern.boottime":
			return okResult("{ sec = 1234, usec = 42 }")
		case "sysctl -n hw.ncpu":
			return okResult("2\n")
		case "sysctl -n hw.physmem":
			return okResult("2147483648\n")
		case "ps -ax -o pid= -o ppid= -o uid= -o rss= -o time= -o lstart= -o comm=":
			return okResult("0 0 0 0 0:00.00 Sat Oct 31 13:00:00 2026 kernel\n1112 1 0 2516 0:01.00 Sat Oct 31 13:10:00 2026 daemon\n1114 1112 1002 15416 0:10.50 Sat Oct 31 13:10:00 2026 /usr/local/sbin/dnsauth\n1115 1114 1002 100 0:00.50 Sat Oct 31 13:10:00 2026 worker\n")
		}
		t.Fatalf("unexpected command: %v", s.Argv)
		return ExecResult{}
	}
	r := report(t, RunOperation(e, "app_memory_v1.sh", []string{"service", "dnsauth", ""}))
	if r.Status != "ok" || len(r.Instances) != 1 {
		t.Fatalf("%+v", r)
	}
	readings := r.Instances[0].Readings
	cpu := readingFor(readings, "cpu_usage_ns")
	if cpu == nil || cpu.Value != 11000000000 || cpu.Method != "process_cputime" || cpu.Accuracy != "live_processes_only" {
		t.Fatalf("%+v", readings)
	}
	if cpu.Capacity == nil || *cpu.Capacity != 2000 || cpu.CapacitySource != "host" {
		t.Fatalf("%+v", cpu)
	}
	rss := readingFor(readings, "memory_rss_bytes")
	if rss.Capacity == nil || *rss.Capacity != 2147483648 || rss.CapacitySource != "host" {
		t.Fatalf("%+v", rss)
	}
}
