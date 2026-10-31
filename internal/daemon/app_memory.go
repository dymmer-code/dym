package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MemoryTarget grants observation only. Service control permissions never imply this grant.
// UID and Executable identify a standalone process; its descendants are included.
type MemoryTarget struct {
	Project    string  `yaml:"project,omitempty"`
	Manager    string  `yaml:"manager"`
	Service    string  `yaml:"service"`
	Path       string  `yaml:"path,omitempty"`
	UID        *uint32 `yaml:"uid,omitempty"`
	Executable string  `yaml:"executable,omitempty"`
	Cgroup     string  `yaml:"cgroup,omitempty"`
}

func (t MemoryTarget) validate() error {
	if !memoryManager(t.Manager) || !ValidName(t.Service) {
		return errors.New("memory: invalid manager or service")
	}
	if t.Project != "" && (t.Manager != "docker_compose" || !ValidName(t.Project)) {
		return errors.New("memory: project is only a valid Compose project name")
	}
	if t.Manager == "docker_compose" {
		if !cleanAbsolute(t.Path) {
			return errors.New("memory: compose path must be clean and absolute")
		}
	} else if t.Path != "" {
		return errors.New("memory: path is only supported for compose")
	}
	if t.Manager == "service" || t.Manager == "init_d" {
		if t.UID == nil || *t.UID == 0 || !ValidName(t.Executable) {
			return errors.New("memory: standalone requires a non-root uid and executable basename")
		}
	} else if t.UID != nil || t.Executable != "" || t.Cgroup != "" {
		return errors.New("memory: process selectors are only supported for service/init_d")
	}
	if t.Cgroup != "" && (!cleanAbsolute(t.Cgroup) || t.Cgroup == "/") {
		return errors.New("memory: cgroup must identify a non-root group")
	}
	return nil
}
func memoryManager(s string) bool {
	return includes([]string{"systemctl", "docker", "docker_compose", "service", "init_d"}, s)
}
func cleanAbsolute(s string) bool {
	return filepath.IsAbs(s) && filepath.Clean(s) == s && !strings.ContainsAny(s, "\x00\r\n") && len(s) <= 1024
}

type MemoryReport struct {
	SchemaVersion int              `json:"schema_version"`
	ObservedAt    string           `json:"observed_at"`
	Status        string           `json:"status"`
	Reason        *string          `json:"reason"`
	Instances     []MemoryInstance `json:"instances"`
}
type MemoryInstance struct {
	InstanceID string          `json:"instance_id"`
	Label      string          `json:"label"`
	Status     string          `json:"status"`
	Reason     *string         `json:"reason"`
	Readings   []MemoryReading `json:"readings"`
}
type MemoryReading struct {
	Metric   string `json:"metric"`
	Method   string `json:"method"`
	Value    int64  `json:"value"`
	Capacity *int64 `json:"capacity"`
	Accuracy string `json:"accuracy"`
}
type memoryError struct{ status, reason string }

func (e memoryError) Error() string        { return e.status + ": " + e.reason }
func memError(status, reason string) error { return memoryError{status, reason} }
func readError(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return memError("permission_denied", "read_denied")
	}
	if errors.Is(err, os.ErrNotExist) {
		return memError("unsupported", "counter_missing")
	}
	return memError("error", "read_failed")
}
func memoryFailure(e error) (string, *string) {
	var me memoryError
	if !errors.As(e, &me) {
		me = memoryError{"error", "read_failed"}
	}
	return me.status, &me.reason
}
func identity(parts ...string) string {
	s := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(s[:])
}

// appMemoryOperation is shared by the local CLI and the fixed daemon operation.
func appMemoryOperation(env OpEnv, args []string) Result {
	if len(args) != 3 || !memoryManager(args[0]) || !ValidName(args[1]) || (args[0] == "docker_compose" && !cleanAbsolute(args[2])) || (args[0] != "docker_compose" && args[2] != "") {
		return fail("invalid memory selector")
	}
	now := time.Now
	if env.Now != nil {
		now = env.Now
	}
	report := MemoryReport{SchemaVersion: 1, ObservedAt: now().UTC().Format(time.RFC3339Nano), Status: "ok", Instances: []MemoryInstance{}}
	config, err := env.config()
	if err != nil {
		return fail("invalid observation configuration")
	}
	var target *MemoryTarget
	for _, t := range config.Memory {
		if t.Manager == args[0] && t.Service == args[1] && t.Path == args[2] {
			v := t
			target = &v
			break
		}
	}
	if target == nil {
		err = memError("permission_denied", "target_not_allowed")
	} else if target.validate() != nil {
		return fail("invalid observation configuration")
	} else if env.GOOS != "linux" && env.GOOS != "freebsd" {
		err = memError("unsupported", "platform")
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c := memoryCollector{env: env, ctx: ctx}
		switch target.Manager {
		case "systemctl":
			report.Instances, err = c.systemd(*target)
		case "docker", "docker_compose":
			report.Instances, err = c.docker(*target)
		default:
			report.Instances, err = c.processes(*target)
		}
	}
	if err != nil {
		report.Status, report.Reason = memoryFailure(err)
		report.Instances = []MemoryInstance{}
	} else {
		for _, i := range report.Instances {
			if i.Status != "ok" {
				report.Status = "partial"
			}
		}
	}
	if err == nil && len(report.Instances) > 0 {
		allStopped := true
		for _, i := range report.Instances {
			if i.Status != "stopped" {
				allStopped = false
			}
		}
		if allStopped {
			report.Status = "stopped"
		}
	}
	if report.Instances == nil {
		report.Instances = []MemoryInstance{}
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > 65536 {
		return fail("memory report exceeds limit")
	}
	return ok(string(raw))
}

// ReadAppMemory performs a local read with exactly the daemon's authorization and contract.
func ReadAppMemory(env OpEnv, manager, service, path string) Result {
	return appMemoryOperation(env, []string{manager, service, path})
}

type memoryCollector struct {
	env OpEnv
	ctx context.Context
}

func (c memoryCollector) run(argv ...string) (string, error) {
	if err := c.ctx.Err(); err != nil {
		return "", memError("error", "timeout")
	}
	if c.env.Exec == nil {
		return "", memError("unsupported", "tool_missing")
	}
	r := c.env.Exec(c.ctx, ExecSpec{Argv: argv, Timeout: 5 * time.Second})
	if r.ExitCode != 0 || r.Err != nil {
		if errors.Is(r.Err, context.DeadlineExceeded) {
			return "", memError("error", "timeout")
		}
		low := strings.ToLower(r.Output)
		if strings.Contains(low, "permission denied") || strings.Contains(low, "access denied") {
			return "", memError("permission_denied", "read_denied")
		}
		if errors.Is(r.Err, os.ErrNotExist) {
			return "", memError("unsupported", "tool_missing")
		}
		return "", memError("error", "command_failed")
	}
	if len(r.Output) >= maxOutput {
		return "", memError("unsupported", "output_limit")
	}
	return r.Output, nil
}
func (c memoryCollector) systemd(t MemoryTarget) ([]MemoryInstance, error) {
	if c.env.GOOS != "linux" {
		return nil, memError("unsupported", "platform")
	}
	argv := []string{"systemctl", "show", "--property=LoadState,ActiveState,MainPID,ControlGroup,InvocationID", "--", t.Service}
	read := func() (map[string]string, error) {
		s, e := c.run(argv...)
		m := map[string]string{}
		for _, l := range strings.Split(s, "\n") {
			k, v, ok := strings.Cut(l, "=")
			if ok {
				m[k] = v
			}
		}
		return m, e
	}
	a, err := read()
	if err != nil {
		return nil, err
	}
	if a["LoadState"] == "not-found" {
		return nil, memError("not_found", "service_missing")
	}
	if a["ActiveState"] != "active" {
		return nil, memError("stopped", "service_inactive")
	}
	pid, e := strconv.Atoi(a["MainPID"])
	if e != nil || pid <= 1 || a["InvocationID"] == "" || !cleanAbsolute(a["ControlGroup"]) || a["ControlGroup"] == "/" {
		return nil, memError("unsupported", "identity_unavailable")
	}
	before, err := linuxPIDIdentity(c.env, pid)
	if err != nil {
		return nil, err
	}
	readings, group, err := readAppCgroup(c.env, pid)
	if err != nil {
		return nil, err
	}
	if group != a["ControlGroup"] {
		return nil, memError("ambiguous", "cgroup_mismatch")
	}
	b, err := read()
	if err != nil {
		return nil, err
	}
	after, err := linuxPIDIdentity(c.env, pid)
	if err != nil || before != after || a["InvocationID"] != b["InvocationID"] || a["MainPID"] != b["MainPID"] || a["ControlGroup"] != b["ControlGroup"] || b["ActiveState"] != "active" {
		return nil, memError("identity_changed", "runtime_changed")
	}
	return []MemoryInstance{{InstanceID: identity(before, a["InvocationID"], group), Label: t.Service, Status: "ok", Readings: readings}}, nil
}

func linuxPIDIdentity(env OpEnv, pid int) (string, error) {
	stat, err := env.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", readError(err)
	}
	end := strings.LastIndex(string(stat), ")")
	if end < 0 {
		return "", memError("error", "invalid_process")
	}
	f := strings.Fields(string(stat)[end+1:])
	if len(f) < 20 {
		return "", memError("error", "invalid_process")
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil {
		return "", memError("error", "invalid_process")
	}
	boot, err := env.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return "", memError("unsupported", "boot_identity_missing")
	}
	return identity(strings.TrimSpace(string(boot)), strconv.Itoa(pid), f[19]), nil
}

// Resolve the memory controller through mountinfo, including non-default mount roots.
func readAppCgroup(env OpEnv, pid int) ([]MemoryReading, string, error) {
	data, err := env.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return nil, "", readError(err)
	}
	groups := map[string]string{}
	for _, l := range strings.Split(string(data), "\n") {
		p := strings.SplitN(l, ":", 3)
		if len(p) != 3 {
			continue
		}
		if p[0] == "0" && p[1] == "" {
			groups["cgroup2"] = p[2]
		}
		if includes(strings.Split(p[1], ","), "memory") {
			groups["cgroup"] = p[2]
		}
	}
	mounts, err := env.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, "", readError(err)
	}
	// A hybrid hierarchy with memory in v1 must use v1, even when unified is mounted.
	kind := "cgroup2"
	if groups["cgroup"] != "" {
		kind = "cgroup"
	}
	group := groups[kind]
	if !cleanAbsolute(group) || group == "/" {
		return nil, "", memError("unsupported", "no_exclusive_cgroup")
	}
	for _, l := range strings.Split(string(mounts), "\n") {
		a, b, ok := strings.Cut(l, " - ")
		if !ok {
			continue
		}
		left, right := strings.Fields(a), strings.Fields(b)
		if len(left) < 6 || len(right) < 3 || right[0] != kind {
			continue
		}
		if kind == "cgroup" && !includes(strings.Split(right[2], ","), "memory") {
			continue
		}
		root, mount := unescapeMount(left[3]), unescapeMount(left[4])
		if !cleanAbsolute(root) || !cleanAbsolute(mount) {
			continue
		}
		if group != root && !strings.HasPrefix(group, strings.TrimSuffix(root, "/")+"/") {
			continue
		}
		dir := filepath.Join(mount, strings.TrimPrefix(group, root))
		valueFile, limitFile, cacheKey, method := "memory.current", "memory.max", "file", "cgroup_v2"
		if kind == "cgroup" {
			valueFile, limitFile, cacheKey, method = "memory.usage_in_bytes", "memory.limit_in_bytes", "total_cache", "cgroup_v1"
		}
		raw, err := env.ReadFile(filepath.Join(dir, valueFile))
		if err != nil {
			return nil, "", readError(err)
		}
		value, err := memoryNumber(string(raw))
		if err != nil {
			return nil, "", err
		}
		r := MemoryReading{Metric: "memory_accounted_bytes", Method: method, Value: value, Accuracy: "kernel_accounted"}
		limit, err := env.ReadFile(filepath.Join(dir, limitFile))
		if err != nil {
			return nil, "", readError(err)
		}
		if strings.TrimSpace(string(limit)) != "max" {
			n, e := memoryNumber(string(limit))
			if e != nil {
				return nil, "", e
			}
			if n > 0 && n < 1<<60 {
				r.Capacity = &n
			}
		}
		out := []MemoryReading{r}
		stats, err := env.ReadFile(filepath.Join(dir, "memory.stat"))
		if err == nil {
			for _, line := range strings.Split(string(stats), "\n") {
				f := strings.Fields(line)
				if len(f) == 2 && f[0] == cacheKey {
					n, e := memoryNumber(f[1])
					if e != nil {
						return nil, "", e
					}
					out = append(out, MemoryReading{Metric: "memory_file_cache_bytes", Method: method, Value: n, Accuracy: "kernel_accounted"})
					break
				}
			}
		}
		return out, group, nil
	}
	return nil, "", memError("unsupported", "memory_controller_missing")
}
func memoryNumber(s string) (int64, error) {
	n, e := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if e != nil || n < 0 {
		return 0, memError("error", "invalid_counter")
	}
	return n, nil
}
