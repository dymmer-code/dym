package daemon

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Inventory is what the daemon tells Dymmer about its host. The names are the ones Dymmer stores.
type Inventory struct {
	Hostname    string   `json:"hostname,omitempty"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
	OSName      string   `json:"os_name,omitempty"`
	OSVersion   string   `json:"os_version,omitempty"`
	CPUName     string   `json:"cpu_name,omitempty"`
	CPUCores    int      `json:"cpu_cores,omitempty"`
	Memory      int64    `json:"memory,omitempty"` // bytes
}

// Payload is the inventory as the map that goes in a channel message.
func (i Inventory) Payload() map[string]any {
	m := map[string]any{}
	set := func(key string, value any, present bool) {
		if present {
			m[key] = value
		}
	}
	set("hostname", i.Hostname, i.Hostname != "")
	set("ip_addresses", i.IPAddresses, len(i.IPAddresses) > 0)
	set("os_name", i.OSName, i.OSName != "")
	set("os_version", i.OSVersion, i.OSVersion != "")
	set("cpu_name", i.CPUName, i.CPUName != "")
	set("cpu_cores", i.CPUCores, i.CPUCores > 0)
	set("memory", i.Memory, i.Memory > 0)
	return m
}

// System is what Collect reads from the machine; tests give their own.
type System struct {
	GOOS, GOARCH string
	NumCPU       int
	Hostname     func() (string, error)
	Addrs        func() ([]net.Addr, error)
	ReadFile     func(path string) ([]byte, error)
	Sysctl       func(name string) (string, error)
}

// HostSystem is the machine the daemon runs on.
func HostSystem() System {
	return System{
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		NumCPU:   runtime.NumCPU(),
		Hostname: os.Hostname,
		Addrs:    net.InterfaceAddrs,
		ReadFile: os.ReadFile,
		Sysctl: func(name string) (string, error) {
			out, err := exec.Command("sysctl", "-n", name).Output()
			return strings.TrimSpace(string(out)), err
		},
	}
}

// Collect reads the inventory. A piece that cannot be read is left out: the daemon is useful with what it has.
func Collect(sys System) Inventory {
	inv := Inventory{CPUCores: sys.NumCPU}
	inv.Hostname, _ = sys.Hostname()
	inv.IPAddresses = Addresses(sys)

	switch sys.GOOS {
	case "linux":
		collectLinux(sys, &inv)
	case "darwin":
		inv.OSName = "macos"
		inv.OSVersion, _ = sys.Sysctl("kern.osproductversion")
		inv.CPUName = joinCPU(machine(sys.GOARCH), firstOf(sys, "machdep.cpu.brand_string"))
		if bytes, err := sys.Sysctl("hw.memsize"); err == nil {
			inv.Memory = atoi64(bytes)
		}
	case "freebsd":
		inv.OSName = "freebsd"
		inv.OSVersion, _ = sys.Sysctl("kern.osrelease")
		inv.CPUName = joinCPU(machine(sys.GOARCH), firstOf(sys, "hw.model"))
		if bytes, err := sys.Sysctl("hw.physmem"); err == nil {
			inv.Memory = atoi64(bytes)
		}
	default:
		inv.OSName = sys.GOOS
	}
	return inv
}

func collectLinux(sys System, inv *Inventory) {
	if data, err := sys.ReadFile("/etc/os-release"); err == nil {
		inv.OSName, inv.OSVersion = ParseOSRelease(string(data))
	}
	if inv.OSName == "" {
		inv.OSName = "linux"
	}
	if data, err := sys.ReadFile("/proc/cpuinfo"); err == nil {
		inv.CPUName = joinCPU(machine(sys.GOARCH), ParseCPUModel(string(data)))
	}
	if data, err := sys.ReadFile("/proc/meminfo"); err == nil {
		inv.Memory = ParseMemTotal(string(data))
	}
}

func firstOf(sys System, name string) string {
	value, _ := sys.Sysctl(name)
	return value
}

// Addresses are the addresses of the host that can say where it is: not loopback, not link-local, not multicast.
func Addresses(sys System) []string {
	addrs, err := sys.Addrs()
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
			continue
		}
		if text := ip.String(); !seen[text] {
			seen[text] = true
			out = append(out, text)
		}
	}
	return out
}

// ParseOSRelease reads /etc/os-release and gives the ID (debian, ubuntu...) and the VERSION_ID.
func ParseOSRelease(text string) (name, version string) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "ID":
			name = value
		case "VERSION_ID":
			version = value
		}
	}
	return name, version
}

// ParseCPUModel reads the model of the CPU from /proc/cpuinfo (x86 says "model name", Arm says "Model" or "Processor").
func ParseCPUModel(text string) string {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name", "Model", "Processor":
			if v := strings.TrimSpace(value); v != "" {
				return v
			}
		}
	}
	return ""
}

// ParseMemTotal reads MemTotal from /proc/meminfo, which is in KiB, and gives bytes.
func ParseMemTotal(text string) int64 {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok || strings.TrimSpace(key) != "MemTotal" {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0
		}
		return atoi64(fields[0]) * 1024
	}
	return 0
}

// machine names the architecture as `uname -m` does, which is how the rest of Dymmer writes it.
func machine(goarch string) string {
	switch goarch {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	case "386":
		return "i686"
	default:
		return goarch
	}
}

func joinCPU(arch, model string) string {
	switch {
	case model == "":
		return arch
	case arch == "":
		return model
	default:
		return arch + " - " + model
	}
}

func atoi64(text string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
