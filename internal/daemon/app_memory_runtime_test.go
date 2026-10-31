package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func pidStat(ticks string) string {
	return "42 (a tricky ) process) S " + strings.Repeat("0 ", 18) + ticks
}
func cgroupFiles() map[string]string {
	return map[string]string{
		"/proc/42/stat": pidStat("123"), "/proc/sys/kernel/random/boot_id": "boot-1",
		"/proc/42/cgroup":      "0::/system.slice/demo.service\n",
		"/proc/self/mountinfo": "25 1 0:23 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n",
		"/sys/fs/cgroup/system.slice/demo.service/memory.current": "16236544",
		"/sys/fs/cgroup/system.slice/demo.service/memory.max":     "max",
		"/sys/fs/cgroup/system.slice/demo.service/memory.stat":    "file 10125312\n",
	}
}
func systemdEnv(files map[string]string, properties string) OpEnv {
	e := memoryEnv(files)
	e.Config = func() (Config, error) {
		return Config{Memory: []MemoryTarget{{Manager: "systemctl", Service: "demo"}}}, nil
	}
	e.Exec = func(_ context.Context, s ExecSpec) ExecResult { return okResult(properties) }
	return e
}

const systemdProperties = "LoadState=loaded\nActiveState=active\nMainPID=42\nControlGroup=/system.slice/demo.service\nInvocationID=run-1\n"

func TestSystemdMemoryIdentityAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, props, status string
		change              func(map[string]string)
	}{
		{"ok", systemdProperties, "ok", nil},
		{"missing", "LoadState=not-found", "not_found", nil},
		{"stopped", "LoadState=loaded\nActiveState=inactive", "stopped", nil},
		{"missing identity", strings.ReplaceAll(systemdProperties, "run-1", ""), "unsupported", nil},
		{"invalid pid", strings.ReplaceAll(systemdProperties, "42", "0"), "unsupported", nil},
		{"shared", strings.ReplaceAll(systemdProperties, "/system.slice/demo.service", "/"), "unsupported", nil},
		{"wrong group", strings.ReplaceAll(systemdProperties, "demo.service", "other.service"), "ambiguous", nil},
		{"bad counter", systemdProperties, "error", func(f map[string]string) { f["/sys/fs/cgroup/system.slice/demo.service/memory.current"] = "-1" }},
		{"bad limit", systemdProperties, "error", func(f map[string]string) { f["/sys/fs/cgroup/system.slice/demo.service/memory.max"] = "huge" }},
		{"bad stat", systemdProperties, "error", func(f map[string]string) { f["/proc/42/stat"] = "oops" }},
		{"missing boot", systemdProperties, "unsupported", func(f map[string]string) { delete(f, "/proc/sys/kernel/random/boot_id") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := cgroupFiles()
			if tc.change != nil {
				tc.change(f)
			}
			r := report(t, ReadAppMemory(systemdEnv(f, tc.props), "systemctl", "demo", ""))
			if r.Status != tc.status {
				t.Fatalf("%+v", r)
			}
			if r.Status == "ok" && r.Instances[0].Readings[0].Value != 16236544 {
				t.Fatal("bad value")
			}
		})
	}
}
func TestSystemdRestartDuringRead(t *testing.T) {
	e := systemdEnv(cgroupFiles(), systemdProperties)
	calls := 0
	e.Exec = func(_ context.Context, _ ExecSpec) ExecResult {
		calls++
		if calls == 2 {
			return okResult(strings.ReplaceAll(systemdProperties, "run-1", "run-2"))
		}
		return okResult(systemdProperties)
	}
	r := report(t, ReadAppMemory(e, "systemctl", "demo", ""))
	if r.Status != "identity_changed" || len(r.Instances) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestCgroupMountRootHybridAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		change       func(map[string]string)
	}{
		{"mount root", "", func(f map[string]string) {
			f["/proc/self/mountinfo"] = "25 1 0:23 /system.slice /custom rw - cgroup2 cgroup rw\n"
			f["/custom/demo.service/memory.current"] = "0"
			f["/custom/demo.service/memory.max"] = "100"
		}},
		{"missing mount", "unsupported", func(f map[string]string) { delete(f, "/proc/self/mountinfo") }},
		{"wrong controller", "unsupported", func(f map[string]string) { f["/proc/self/mountinfo"] = "25 1 0:23 / /sys/fs/cgroup rw - tmpfs x rw\n" }},
		{"traversal", "unsupported", func(f map[string]string) { f["/proc/42/cgroup"] = "0::/../etc\n" }},
		{"missing counter", "unsupported", func(f map[string]string) { delete(f, "/sys/fs/cgroup/system.slice/demo.service/memory.current") }},
		{"missing limit", "unsupported", func(f map[string]string) { delete(f, "/sys/fs/cgroup/system.slice/demo.service/memory.max") }},
		{"invalid cache", "error", func(f map[string]string) { f["/sys/fs/cgroup/system.slice/demo.service/memory.stat"] = "file nope" }},
		{"missing cgroup", "unsupported", func(f map[string]string) { delete(f, "/proc/42/cgroup") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := cgroupFiles()
			tc.change(f)
			r, _, e := readAppCgroup(memoryEnv(f), 42)
			if tc.status == "" {
				if e != nil || r[0].Value != 0 || *r[0].Capacity != 100 {
					t.Fatalf("%+v %v", r, e)
				}
			} else {
				s, _ := memoryFailure(e)
				if e == nil || s != tc.status {
					t.Fatalf("%v", e)
				}
			}
		})
	}
}
func TestMemoryCommandFailuresAreSanitized(t *testing.T) {
	for _, tc := range []struct {
		r      ExecResult
		status string
	}{{ExecResult{ExitCode: 1, Output: "permission denied secret"}, "permission_denied"}, {ExecResult{ExitCode: -1, Err: os.ErrNotExist}, "unsupported"}, {ExecResult{ExitCode: -1, Err: context.DeadlineExceeded}, "error"}, {ExecResult{ExitCode: 1, Output: "secret"}, "error"}, {ExecResult{Output: strings.Repeat("x", 65536)}, "unsupported"}} {
		e := systemdEnv(cgroupFiles(), systemdProperties)
		e.Exec = func(_ context.Context, _ ExecSpec) ExecResult { return tc.r }
		r := report(t, ReadAppMemory(e, "systemctl", "demo", ""))
		if r.Status != tc.status || strings.Contains(fmt.Sprint(r), "secret") {
			t.Fatalf("%+v", r)
		}
	}
	e := systemdEnv(nil, "")
	e.ReadFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	e.Exec = func(_ context.Context, _ ExecSpec) ExecResult { return okResult(systemdProperties) }
	if r := report(t, ReadAppMemory(e, "systemctl", "demo", "")); r.Status != "permission_denied" {
		t.Fatalf("%+v", r)
	}
	e.Config = func() (Config, error) { return Config{}, errors.New("secret") }
	if r := ReadAppMemory(e, "systemctl", "demo", ""); r.Status != "FAIL" || strings.Contains(r.Message, "secret") {
		t.Fatal(r)
	}
}

func dockerEnv(files map[string]string, containers []memoryContainer) OpEnv {
	e := memoryEnv(files)
	e.Config = func() (Config, error) {
		return Config{Memory: []MemoryTarget{{Manager: "docker", Service: "demo"}, {Manager: "docker_compose", Service: "web", Path: "/srv/app", Project: "app"}}}, nil
	}
	e.Exec = func(_ context.Context, s ExecSpec) ExecResult {
		if s.Argv[1] == "ps" {
			ids := []string{}
			for _, c := range containers {
				if c.ID == "" {
					ids = append(ids, strings.Repeat("c", 64))
				} else {
					ids = append(ids, c.ID)
				}
			}
			return okResult(strings.Join(ids, "\n"))
		}
		for _, c := range containers {
			if c.ID == s.Argv[len(s.Argv)-1] {
				b, _ := json.Marshal(c)
				return okResult(string(b))
			}
		}
		return ExecResult{ExitCode: 1}
	}
	return e
}
func TestDockerMemoryAndReplicaStates(t *testing.T) {
	id := strings.Repeat("a", 64)
	f := cgroupFiles()
	dir := "/sys/fs/cgroup/docker/" + id
	f["/proc/42/cgroup"] = "0::/docker/" + id
	f[dir+"/memory.current"] = "100"
	f[dir+"/memory.max"] = "1000"
	running := memoryContainer{ID: id, Name: "/demo", PID: 42, Running: true, Started: "2026-10-31T00:00:00Z", Project: "app", Service: "web", Path: "/srv/app"}
	r := report(t, ReadAppMemory(dockerEnv(f, []memoryContainer{running}), "docker", "demo", ""))
	if r.Status != "ok" || r.Instances[0].Readings[0].Value != 100 {
		t.Fatalf("%+v", r)
	}
	stopped := running
	stopped.ID = strings.Repeat("b", 64)
	stopped.Running = false
	stopped.Name = "/app_web_2"
	r = report(t, ReadAppMemory(dockerEnv(f, []memoryContainer{running, stopped}), "docker_compose", "web", "/srv/app"))
	if r.Status != "partial" || len(r.Instances) != 2 || len(r.Instances[1].Readings) != 0 {
		t.Fatalf("%+v", r)
	}
	for _, tc := range []struct {
		name, status string
		containers   []memoryContainer
	}{
		{"none", "not_found", nil}, {"bad id", "error", []memoryContainer{{ID: "oops"}}}, {"wrong name", "ambiguous", []memoryContainer{{ID: id, Name: "/other"}}}, {"duplicates", "error", []memoryContainer{running, running}}, {"too many", "unsupported", make([]memoryContainer, 33)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := report(t, ReadAppMemory(dockerEnv(f, tc.containers), "docker", "demo", ""))
			if r.Status != tc.status {
				t.Fatalf("%+v", r)
			}
		})
	}
	running.Path = "/other"
	if r := report(t, ReadAppMemory(dockerEnv(f, []memoryContainer{running}), "docker_compose", "web", "/srv/app")); r.Status != "ambiguous" {
		t.Fatalf("%+v", r)
	}
}
func TestProcessIdentityAmbiguityAndChurn(t *testing.T) {
	uid := uint32(1002)
	row := "42 1 1002 10 Sat Oct 31 13:10:00 2026 app\n"
	target := MemoryTarget{Manager: "service", Service: "demo", UID: &uid, Executable: "app"}
	for _, tc := range []struct{ name, first, second, status string }{
		{"good", row, row, "ok"}, {"missing", "", "", "not_found"}, {"multiple", row + strings.Replace(row, "42 1", "43 1", 1), "", "ambiguous"},
		{"reused pid", row, strings.Replace(row, "13:10:00", "13:11:00", 1), "identity_changed"}, {"new child", row, row + "43 42 1002 5 Sat Oct 31 13:10:00 2026 worker\n", "identity_changed"}, {"bad rss", strings.Replace(row, "1002 10", "1002 -1", 1), "", "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := memoryEnv(cgroupFiles())
			e.Config = func() (Config, error) { return Config{Memory: []MemoryTarget{target}}, nil }
			calls := 0
			e.Exec = func(_ context.Context, _ ExecSpec) ExecResult {
				calls++
				if calls == 1 {
					return okResult(tc.first)
				}
				return okResult(tc.second)
			}
			r := report(t, ReadAppMemory(e, "service", "demo", ""))
			if r.Status != tc.status {
				t.Fatalf("%+v", r)
			}
		})
	}
}
