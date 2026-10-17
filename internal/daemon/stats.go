package daemon

import (
	"bufio"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// VolumeUsage is how full a volume is and, when the kernel counts them, the reads and writes it has done.
type VolumeUsage struct {
	Device string
	Size   int64  // bytes, the whole volume
	Used   int64  // bytes
	Reads  *int64 // completed reads since boot
	Writes *int64 // completed writes since boot
}

// Stats is a sample of how the host is doing. The names are the ones Dymmer stores (its host statistics).
type Stats struct {
	CPULoad     float64 // load average of the last minute
	MemoryUsage int64   // bytes in use (total less what is available)
	Uptime      int64   // seconds
	NetworkIn   int64   // bytes received since boot, every interface but loopback
	NetworkOut  int64   // bytes sent since boot, every interface but loopback
	Volumes     []VolumeUsage
}

// Payload is the sample as the map that goes in a channel message.
func (s Stats) Payload() map[string]any {
	volumes := make([]map[string]any, 0, len(s.Volumes))
	for _, v := range s.Volumes {
		m := map[string]any{"device": v.Device, "size": v.Size, "used": v.Used}
		if v.Reads != nil {
			m["reads"] = *v.Reads
		}
		if v.Writes != nil {
			m["writes"] = *v.Writes
		}
		volumes = append(volumes, m)
	}
	return map[string]any{
		"cpu_load":     s.CPULoad,
		"memory_usage": s.MemoryUsage,
		"uptime":       s.Uptime,
		"network_in":   s.NetworkIn,
		"network_out":  s.NetworkOut,
		"volumes":      volumes,
	}
}

// StatsSystem is what CollectStats reads from the machine; tests give their own.
type StatsSystem struct {
	GOOS     string
	ReadFile func(path string) ([]byte, error)
	// Statfs gives the size and the free space of the file system mounted at path, in bytes.
	Statfs func(path string) (total, free uint64, err error)
	// Run runs a command and gives its output (FreeBSD is read through sysctl, netstat and df).
	Run func(name string, args ...string) (string, error)
	// Sleep waits; the operations that take two samples use it (tests do not wait).
	Sleep func(time.Duration)
	// Now is the clock; the uptime of FreeBSD is the time since the boot it reports.
	Now func() time.Time
	// ResolveDevice follows /dev/mapper/x and similar links to the name the kernel counts I/O under (dm-0).
	ResolveDevice func(device string) string
}

// HostStatsSystem is the machine the daemon runs on.
func HostStatsSystem(goos string) StatsSystem {
	return StatsSystem{GOOS: goos, ReadFile: readFile, Statfs: statfs, ResolveDevice: resolveDevice, Run: run, Now: time.Now, Sleep: time.Sleep}
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func resolveDevice(device string) string {
	if real, err := filepath.EvalSymlinks(device); err == nil {
		return real
	}
	return device
}

// CollectStats takes a sample. The second result is false when this system is not supported (Linux and FreeBSD are)
// or when not even the basics could be read: the daemon then sends nothing rather than made-up numbers.
func CollectStats(sys StatsSystem) (Stats, bool) {
	switch sys.GOOS {
	case "linux":
		return collectLinuxStats(sys)
	case "freebsd":
		return collectFreeBSDStats(sys)
	default:
		return Stats{}, false
	}
}

func collectLinuxStats(sys StatsSystem) (Stats, bool) {
	var s Stats
	got := 0

	if data, err := sys.ReadFile("/proc/loadavg"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if load, err := strconv.ParseFloat(fields[0], 64); err == nil {
				s.CPULoad = load
				got++
			}
		}
	}
	if data, err := sys.ReadFile("/proc/meminfo"); err == nil {
		total, available := meminfo(string(data), "MemTotal"), meminfo(string(data), "MemAvailable")
		if total > 0 && available > 0 && available <= total {
			s.MemoryUsage = total - available
			got++
		}
	}
	if data, err := sys.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			if up, err := strconv.ParseFloat(fields[0], 64); err == nil {
				s.Uptime = int64(up)
				got++
			}
		}
	}
	if data, err := sys.ReadFile("/proc/net/dev"); err == nil {
		s.NetworkIn, s.NetworkOut = netTotals(string(data))
	}
	s.Volumes = volumes(sys)

	// Without any of the three basic numbers there is no sample worth sending.
	return s, got > 0
}

// meminfo reads one entry of /proc/meminfo, which is in KiB, and gives bytes.
func meminfo(text, key string) int64 {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		k, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		if fields := strings.Fields(value); len(fields) > 0 {
			return atoi64(fields[0]) * 1024
		}
	}
	return 0
}

// netTotals adds the bytes received and sent by every interface but loopback (/proc/net/dev: after the name,
// the first column is bytes received and the ninth is bytes sent).
func netTotals(text string) (in, out int64) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		name, counters, ok := strings.Cut(scanner.Text(), ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(counters)
		if len(fields) < 9 {
			continue
		}
		in += atoi64(fields[0])
		out += atoi64(fields[8])
	}
	return in, out
}

// volumes lists the block-device file systems that are mounted, once each, with their use and their I/O counters.
// Loop and fuse devices are left out, as Dymmer always did.
func volumes(sys StatsSystem) []VolumeUsage {
	mounts, err := sys.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	io := diskIO(sys)

	var out []VolumeUsage
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(mounts)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		device, mountpoint := fields[0], unescapeMount(fields[1])
		if !strings.HasPrefix(device, "/dev/") || strings.HasPrefix(device, "/dev/loop") || strings.HasPrefix(device, "/dev/fuse") || seen[device] {
			continue
		}
		seen[device] = true

		total, free, err := sys.Statfs(mountpoint)
		if err != nil || free > total {
			continue
		}
		v := VolumeUsage{Device: device, Size: int64(total), Used: int64(total - free)}
		if counters, ok := io[filepath.Base(sys.resolve(device))]; ok {
			reads, writes := counters[0], counters[1]
			v.Reads, v.Writes = &reads, &writes
		}
		out = append(out, v)
	}
	return out
}

func (sys StatsSystem) resolve(device string) string {
	if sys.ResolveDevice == nil {
		return device
	}
	return sys.ResolveDevice(device)
}

// diskIO gives {reads, writes} completed since boot for every device in /proc/diskstats.
func diskIO(sys StatsSystem) map[string][2]int64 {
	out := map[string][2]int64{}
	data, err := sys.ReadFile("/proc/diskstats")
	if err != nil {
		return out
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}
		out[fields[2]] = [2]int64{atoi64(fields[3]), atoi64(fields[7])}
	}
	return out
}

// unescapeMount undoes the octal escapes /proc/mounts uses for spaces and the like (\040).
func unescapeMount(path string) string {
	if !strings.Contains(path, `\`) {
		return path
	}
	var b strings.Builder
	for i := 0; i < len(path); i++ {
		if path[i] == '\\' && i+3 < len(path)+0 && i+3 <= len(path)-1+1 {
			if n, err := strconv.ParseUint(path[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(path[i])
	}
	return b.String()
}

// currentGOOS is the system the daemon runs on.
var currentGOOS = runtime.GOOS
