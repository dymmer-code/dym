package daemon

import (
	"encoding/json"
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type memoryContainer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	PID     int    `json:"pid"`
	Running bool   `json:"running"`
	Started string `json:"started"`
	Project string `json:"project"`
	Service string `json:"service"`
	Path    string `json:"path"`
}

const memoryInspectFormat = `{"id":{{json .Id}},"name":{{json .Name}},"pid":{{json .State.Pid}},"running":{{json .State.Running}},"started":{{json .State.StartedAt}},"project":{{json (index .Config.Labels "com.docker.compose.project")}},"service":{{json (index .Config.Labels "com.docker.compose.service")}},"path":{{json (index .Config.Labels "com.docker.compose.project.working_dir")}}}`

func (c memoryCollector) inspect(name string) (memoryContainer, error) {
	s, e := c.run("docker", "inspect", "--type", "container", "--format", memoryInspectFormat, "--", name)
	var v memoryContainer
	if e != nil {
		return v, e
	}
	if json.Unmarshal([]byte(s), &v) != nil || len(v.ID) != 64 || !hexID(v.ID) {
		return v, memError("error", "invalid_container")
	}
	return v, nil
}
func hexID(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return s != ""
}
func (c memoryCollector) docker(t MemoryTarget) ([]MemoryInstance, error) {
	if c.env.GOOS != "linux" {
		return nil, memError("unsupported", "platform")
	}
	argv := []string{"docker", "ps", "--all", "--quiet", "--no-trunc"}
	if t.Manager == "docker_compose" {
		argv = append(argv, "--filter", "label=com.docker.compose.service="+t.Service)
		if t.Project != "" {
			argv = append(argv, "--filter", "label=com.docker.compose.project="+t.Project)
		} else {
			argv = append(argv, "--filter", "label=com.docker.compose.project.working_dir="+t.Path)
		}
	} else {
		argv = append(argv, "--filter", "name=^/"+regexp.QuoteMeta(t.Service)+"$")
	}
	s, e := c.run(argv...)
	if e != nil {
		return nil, e
	}
	ids := strings.Fields(s)
	if len(ids) == 0 {
		return nil, memError("not_found", "container_missing")
	}
	if len(ids) > 32 {
		return nil, memError("unsupported", "instance_limit")
	}
	out := make([]MemoryInstance, 0, len(ids))
	project := ""
	seen := map[string]bool{}
	for _, id := range ids {
		if len(id) != 64 || !hexID(id) || seen[id] {
			return nil, memError("error", "invalid_container_list")
		}
		seen[id] = true
		a, e := c.inspect(id)
		if e != nil {
			return nil, e
		}
		if a.ID != id {
			return nil, memError("identity_changed", "runtime_changed")
		}
		if t.Manager == "docker_compose" {
			if (a.Path != "" && a.Path != t.Path) || (a.Path == "" && t.Project == "") || (t.Project != "" && a.Project != t.Project) || a.Service != t.Service || a.Project == "" {
				return nil, memError("ambiguous", "compose_mismatch")
			}
			if project != "" && project != a.Project {
				return nil, memError("ambiguous", "compose_collision")
			}
			project = a.Project
		} else if strings.TrimPrefix(a.Name, "/") != t.Service && a.ID != t.Service && !strings.HasPrefix(a.ID, t.Service) {
			return nil, memError("ambiguous", "container_mismatch")
		}
		inst := MemoryInstance{InstanceID: identity(a.ID, a.Started), Label: strings.TrimPrefix(a.Name, "/"), Status: "ok", Readings: []MemoryReading{}}
		if len(inst.Label) > 256 {
			return nil, memError("unsupported", "label_limit")
		}
		if !a.Running {
			inst.Status = "stopped"
			reason := "container_inactive"
			inst.Reason = &reason
			out = append(out, inst)
			continue
		}
		inst.Readings, e = c.containerReading(a)
		if e != nil {
			inst.Status, inst.Reason = memoryFailure(e)
			inst.Readings = []MemoryReading{}
		}
		out = append(out, inst)
	}
	return out, nil
}
func (c memoryCollector) containerReading(a memoryContainer) ([]MemoryReading, error) {
	if a.PID <= 1 || a.Started == "" {
		return nil, memError("unsupported", "identity_unavailable")
	}
	before, e := linuxPIDIdentity(c.env, a.PID)
	if e != nil {
		return nil, e
	}
	readings, group, e := readAppCgroup(c.env, a.PID)
	if e != nil {
		return nil, e
	}
	belongs := false
	for _, p := range strings.Split(group, "/") {
		if p == a.ID || p == "docker-"+a.ID+".scope" {
			belongs = true
		}
	}
	if !belongs {
		return nil, memError("ambiguous", "container_cgroup_mismatch")
	}
	b, e := c.inspect(a.ID)
	if e != nil {
		return nil, e
	}
	after, e := linuxPIDIdentity(c.env, a.PID)
	if e != nil || before != after || a.PID != b.PID || a.Started != b.Started || a.ID != b.ID || !b.Running {
		return nil, memError("identity_changed", "runtime_changed")
	}
	return readings, nil
}

type memoryProcess struct {
	pid, ppid   int
	uid         uint32
	rss         int64
	start, comm string
}

func (c memoryCollector) processSnapshot() (map[int]memoryProcess, error) {
	s, e := c.run("ps", "-ax", "-o", "pid=", "-o", "ppid=", "-o", "uid=", "-o", "rss=", "-o", "lstart=", "-o", "comm=")
	if e != nil {
		return nil, e
	}
	out := map[int]memoryProcess{}
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 10 {
			return nil, memError("error", "invalid_process_list")
		}
		pid, e1 := strconv.Atoi(f[0])
		ppid, e2 := strconv.Atoi(f[1])
		uid, e3 := strconv.ParseUint(f[2], 10, 32)
		rss, e4 := strconv.ParseInt(f[3], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || pid < 0 || ppid < 0 || rss < 0 || rss > math.MaxInt64/1024 {
			return nil, memError("error", "invalid_process_list")
		}
		if pid == 0 {
			continue
		}
		if _, dup := out[pid]; dup {
			return nil, memError("error", "invalid_process_list")
		}
		out[pid] = memoryProcess{pid: pid, ppid: ppid, uid: uint32(uid), rss: rss * 1024, start: strings.Join(f[4:9], " "), comm: strings.Join(f[9:], " ")}
		if len(out) > 4096 {
			return nil, memError("unsupported", "process_limit")
		}
	}
	return out, nil
}
func processTree(all map[int]memoryProcess, root int) (map[int]memoryProcess, error) {
	selected := map[int]memoryProcess{root: all[root]}
	for changed := true; changed; {
		changed = false
		for pid, p := range all {
			if _, ok := selected[pid]; ok {
				continue
			}
			if _, ok := selected[p.ppid]; ok {
				selected[pid] = p
				changed = true
				if len(selected) > 1024 {
					return nil, memError("unsupported", "process_limit")
				}
			}
		}
	}
	return selected, nil
}
func selectRoot(all map[int]memoryProcess, t MemoryTarget) (int, error) {
	candidates := map[int]bool{}
	for pid, p := range all {
		if p.uid == *t.UID && filepath.Base(p.comm) == t.Executable && pid > 1 {
			candidates[pid] = true
		}
	}
	roots := []int{}
	for pid := range candidates {
		p := all[pid]
		seen := map[int]bool{pid: true}
		child := false
		for p.ppid > 1 {
			if seen[p.ppid] {
				return 0, memError("ambiguous", "process_cycle")
			}
			seen[p.ppid] = true
			if candidates[p.ppid] {
				child = true
				break
			}
			parent, ok := all[p.ppid]
			if !ok {
				break
			}
			p = parent
		}
		if !child {
			roots = append(roots, pid)
		}
	}
	if len(roots) == 0 {
		return 0, memError("not_found", "process_missing")
	}
	if len(roots) != 1 {
		return 0, memError("ambiguous", "multiple_roots")
	}
	return roots[0], nil
}
func (c memoryCollector) processes(t MemoryTarget) ([]MemoryInstance, error) {
	if t.UID == nil {
		return nil, memError("unsupported", "identity_unavailable")
	}
	a, e := c.processSnapshot()
	if e != nil {
		return nil, e
	}
	root, e := selectRoot(a, t)
	if e != nil {
		return nil, e
	}
	tree, e := processTree(a, root)
	if e != nil {
		return nil, e
	}
	ids := map[int]string{}
	boot := ""
	if c.env.GOOS == "freebsd" {
		boot, e = c.run("sysctl", "-n", "kern.boottime")
		if e != nil {
			return nil, e
		}
		if !strings.Contains(boot, "sec =") {
			return nil, memError("unsupported", "boot_identity_missing")
		}
	}
	total := int64(0)
	for pid, p := range tree {
		if total > math.MaxInt64-p.rss {
			return nil, memError("error", "invalid_counter")
		}
		total += p.rss
		if c.env.GOOS == "linux" {
			ids[pid], e = linuxPIDIdentity(c.env, pid)
			if e != nil {
				return nil, e
			}
		} else {
			ids[pid] = identity(boot, strconv.Itoa(pid), p.start, strconv.FormatUint(uint64(p.uid), 10), p.comm)
		}
	}
	readings := []MemoryReading{{Metric: "memory_rss_bytes", Method: "process_rss", Value: total, Accuracy: "shared_pages_may_repeat"}}
	if t.Cgroup != "" {
		if c.env.GOOS != "linux" {
			return nil, memError("unsupported", "platform")
		}
		var group string
		readings, group, e = readAppCgroup(c.env, root)
		if e != nil {
			return nil, e
		}
		if group != t.Cgroup {
			return nil, memError("ambiguous", "cgroup_mismatch")
		}
	}
	b, e := c.processSnapshot()
	if e != nil {
		return nil, e
	}
	root2, e := selectRoot(b, t)
	if e != nil || root2 != root {
		return nil, memError("identity_changed", "runtime_changed")
	}
	tree2, e := processTree(b, root)
	if e != nil || len(tree) != len(tree2) {
		return nil, memError("identity_changed", "runtime_changed")
	}
	for pid, p := range tree {
		q, ok := tree2[pid]
		if !ok || p.start != q.start || p.uid != q.uid || p.comm != q.comm || p.ppid != q.ppid {
			return nil, memError("identity_changed", "runtime_changed")
		}
		if c.env.GOOS == "linux" {
			id, e := linuxPIDIdentity(c.env, pid)
			if e != nil || id != ids[pid] {
				return nil, memError("identity_changed", "runtime_changed")
			}
		}
	}
	// Membership changes form a new segment; do not join an altered worker set silently.
	keys := make([]int, 0, len(ids))
	for pid := range ids {
		keys = append(keys, pid)
	}
	sort.Ints(keys)
	parts := []string{}
	for _, pid := range keys {
		parts = append(parts, ids[pid])
	}
	return []MemoryInstance{{InstanceID: identity(parts...), Label: t.Service, Status: "ok", Readings: readings}}, nil
}
