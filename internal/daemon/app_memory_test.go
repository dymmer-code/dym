package daemon

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func memoryEnv(files map[string]string) OpEnv {
	return OpEnv{StatsSystem: StatsSystem{GOOS: "linux", Now: func() time.Time { return time.Date(2026, 10, 31, 15, 0, 0, 0, time.UTC) }, ReadFile: func(p string) ([]byte, error) {
		if v, ok := files[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}}, Config: func() (Config, error) { return Config{}, nil }}
}
func report(t *testing.T, r Result) MemoryReport {
	t.Helper()
	if r.Status != "OK" {
		t.Fatalf("%+v", r)
	}
	var v MemoryReport
	if err := json.Unmarshal([]byte(r.Message), &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func TestAppMemoryRequiresSeparateAuthorization(t *testing.T) {
	e := memoryEnv(nil)
	e.Config = func() (Config, error) { return Config{Services: map[string][]string{"systemctl": {"demo"}}}, nil }
	r := report(t, RunOperation(e, "app_memory_v1.sh", []string{"systemctl", "demo", ""}))
	if r.Status != "permission_denied" || len(r.Instances) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestAppMemoryRejectsUntrustedSelectors(t *testing.T) {
	for _, a := range [][]string{{}, {"systemctl", "../demo", ""}, {"docker_compose", "web", "/srv/../tmp"}, {"custom", "demo", ""}} {
		if r := RunOperation(memoryEnv(nil), "app_memory_v1.sh", a); r.Status != "FAIL" {
			t.Fatalf("accepted %v", a)
		}
	}
}
func TestCgroupMemoryVersionsAndNoHostCapacity(t *testing.T) {
	for _, version := range []string{"cgroup", "cgroup2"} {
		t.Run(version, func(t *testing.T) {
			files := map[string]string{"/proc/42/cgroup": "0::/apps/demo\n", "/proc/self/mountinfo": "25 1 0:23 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n", "/sys/fs/cgroup/apps/demo/memory.current": "16236544", "/sys/fs/cgroup/apps/demo/memory.max": "max", "/sys/fs/cgroup/apps/demo/memory.stat": "file 10125312\n"}
			if version == "cgroup" {
				files["/proc/42/cgroup"] = "2:memory:/apps/demo\n"
				files["/proc/self/mountinfo"] = "25 1 0:23 / /sys/fs/cgroup/memory rw - cgroup cgroup rw,memory\n"
				files["/sys/fs/cgroup/memory/apps/demo/memory.usage_in_bytes"] = "375103488"
				files["/sys/fs/cgroup/memory/apps/demo/memory.limit_in_bytes"] = "4294967296"
				files["/sys/fs/cgroup/memory/apps/demo/memory.stat"] = "total_cache 213651456\n"
			}
			readings, _, err := readAppCgroup(memoryEnv(files), 42)
			if err != nil {
				t.Fatal(err)
			}
			if len(readings) != 2 || readings[0].Metric != "memory_accounted_bytes" {
				t.Fatalf("%+v", readings)
			}
			if version == "cgroup2" && readings[0].Capacity != nil {
				t.Fatal("invented capacity")
			}
			if version == "cgroup" && (readings[0].Capacity == nil || *readings[0].Capacity != 4294967296) {
				t.Fatal("missing limit")
			}
		})
	}
}
func TestCgroupMalformedAndSharedRoot(t *testing.T) {
	e := memoryEnv(map[string]string{"/proc/42/cgroup": "0::/\n", "/proc/self/mountinfo": "25 1 0:23 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n"})
	if _, _, err := readAppCgroup(e, 42); err == nil {
		t.Fatal("accepted host root")
	}
}

func TestMemoryPolicyValidation(t *testing.T) {
	uid := uint32(1002)
	good := MemoryTarget{Manager: "service", Service: "dnsauth", UID: &uid, Executable: "dnsauth"}
	if err := (Config{Memory: []MemoryTarget{good}}).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, v := range []MemoryTarget{{Manager: "service", Service: "x"}, {Manager: "custom", Service: "x"}, {Manager: "docker_compose", Service: "php", Path: "relative"}, {Manager: "systemctl", Service: "x", UID: &uid}, {Manager: "service", Service: "x", UID: &uid, Executable: "x", Cgroup: "/"}} {
		if err := (Config{Memory: []MemoryTarget{v}}).Validate(); err == nil {
			t.Fatalf("accepted %+v", v)
		}
	}
	if err := (Config{Memory: []MemoryTarget{good, good}}).Validate(); err == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestFreeBSDProcessMemoryExcludesSupervisor(t *testing.T) {
	uid := uint32(1002)
	e := memoryEnv(nil)
	e.GOOS = "freebsd"
	e.Config = func() (Config, error) {
		return Config{Memory: []MemoryTarget{{Manager: "service", Service: "dnsauth", UID: &uid, Executable: "dnsauth"}}}, nil
	}
	e.Exec = func(_ context.Context, s ExecSpec) ExecResult {
		if s.Argv[0] == "sysctl" {
			return okResult("{ sec = 1234, usec = 42 }")
		}
		if strings.Join(s.Argv, " ") != "ps -ax -o pid= -o ppid= -o uid= -o rss= -o lstart= -o comm=" {
			t.Fatalf("non-portable ps command: %v", s.Argv)
		}
		return okResult("0 0 0 0 Sat Oct 31 13:00:00 2026 kernel\n1112 1 0 2516 Sat Oct 31 13:10:00 2026 daemon\n1114 1112 1002 15416 Sat Oct 31 13:10:00 2026 /usr/local/sbin/dnsauth\n1115 1114 1002 100 Sat Oct 31 13:10:00 2026 worker\n")
	}
	r := report(t, RunOperation(e, "app_memory_v1.sh", []string{"service", "dnsauth", ""}))
	if r.Status != "ok" || len(r.Instances) != 1 {
		t.Fatalf("%+v", r)
	}
	v := r.Instances[0].Readings[0]
	if v.Value != 15516*1024 || v.Metric != "memory_rss_bytes" || v.Capacity != nil {
		t.Fatalf("%+v", v)
	}
}

func TestLegacyComposeUsesExplicitProjectBinding(t *testing.T) {
	id := strings.Repeat("a", 64)
	e := memoryEnv(nil)
	e.Config = func() (Config, error) {
		return Config{Memory: []MemoryTarget{{Manager: "docker_compose", Service: "rabbitmq", Path: "/srv/project", Project: "project"}}}, nil
	}
	e.Exec = func(_ context.Context, s ExecSpec) ExecResult {
		if s.Argv[1] == "ps" {
			if !strings.Contains(strings.Join(s.Argv, " "), "label=com.docker.compose.project=project") {
				t.Fatal("missing explicit project binding")
			}
			return okResult(id)
		}
		return okResult(`{"id":"` + id + `","name":"/project_rabbitmq_1","pid":0,"running":false,"started":"2025-01-01T00:00:00Z","project":"project","service":"rabbitmq","path":""}`)
	}
	r := report(t, RunOperation(e, "app_memory_v1.sh", []string{"docker_compose", "rabbitmq", "/srv/project"}))
	if r.Status != "stopped" || len(r.Instances) != 1 || len(r.Instances[0].Readings) != 0 {
		t.Fatalf("%+v", r)
	}
}
