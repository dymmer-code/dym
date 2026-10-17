package daemon

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Result is what an operation answers: the shape the scripts of Dymmer print, so that Dymmer reads it as it always did.
// Output and Service are for the operations on services, whose scripts also printed them.
type Result struct {
	Status  string // "OK" or "FAIL"
	Message string
	Output  string // "OK" (running), "NO" (stopped), "ERROR" or "UNKNOWN"
	Service string
}

func ok(message string) Result   { return Result{Status: "OK", Message: message} }
func fail(message string) Result { return Result{Status: "FAIL", Message: message} }

// Payload is the result as the map that goes in a channel message.
func (r Result) Payload() map[string]any {
	m := map[string]any{"status": r.Status, "message": r.Message}
	if r.Output != "" {
		m["output"] = r.Output
	}
	if r.Service != "" {
		m["service"] = r.Service
	}
	return m
}

// OpEnv is what an operation can use: the machine it reads, a way to run a command, and the configuration of the owner.
type OpEnv struct {
	StatsSystem
	Exec   ExecFunc
	Stream StreamFunc
	Config func() (Config, error)
}

// config reads the configuration of the owner; an error in it is a failure of the operation that needs it.
func (e OpEnv) config() (Config, error) {
	if e.Config == nil {
		return Config{}, nil
	}
	return e.Config()
}

// HostOpEnv is the host itself, with the configuration read from path.
func HostOpEnv(goos, configPath string) OpEnv {
	return OpEnv{
		StatsSystem: HostStatsSystem(goos),
		Exec:        HostExec,
		Stream:      HostStream,
		Config:      func() (Config, error) { return LoadConfig(configPath) },
	}
}

// Operation is something Dymmer can ask this host to do, by name. It is a fixed implementation that lives in this
// program for each system: Dymmer sends a name and arguments, never a script or a command line, and an operation
// that is not here is refused. Arguments are checked by the operation before it does anything.
type Operation func(env OpEnv, args []string) Result

// Operations are the operations that this host answers. They are named like the scripts Dymmer used to send, so that
// a host reached by SSH and one reached through the daemon are asked the same way.
func Operations() map[string]Operation {
	return map[string]Operation{
		"cpu_time_seconds_total.sh":       noArgs(cpuTime),
		"cpu_usage_percent.sh":            noArgs(cpuUsage),
		"disk_io_duration_seconds.sh":     noArgs(diskIODuration),
		"disk_io_reads_total.sh":          noArgs(diskIOReads),
		"disk_io_writes_total.sh":         noArgs(diskIOWrites),
		"disk_used_bytes.sh":              noArgs(diskUsed),
		"memory_cached_bytes.sh":          noArgs(memoryCached),
		"memory_used_bytes.sh":            noArgs(memoryUsed),
		"network_active_connections.sh":   noArgs(networkConnections),
		"network_receive_bytes_total.sh":  noArgs(networkReceived),
		"network_transmit_bytes_total.sh": noArgs(networkTransmitted),
		"system_uptime_seconds.sh":        noArgs(uptime),

		// Services, by name; what changes one is only what the configuration of the owner allows.
		"systemctl.sh":      serviceOperation(systemctlManager),
		"service.sh":        serviceOperation(serviceManager),
		"init_d.sh":         serviceOperation(initDManager),
		"docker.sh":         serviceOperation(dockerManager),
		"pm2.sh":            serviceOperation(pm2Manager),
		"supervisord.sh":    serviceOperation(supervisordManager),
		"docker_compose.sh": composeOperation,

		// What the owner wrote, by name, and what the owner allowed to be read.
		"action.sh":              actionOperation,
		"log_recent_matching.sh": logOperation,
	}
}

// OperationNames lists them, sorted, for the host to tell Dymmer which ones it answers.
func OperationNames() []string {
	names := make([]string, 0, len(Operations())+len(Streams()))
	for name := range Operations() {
		names = append(names, name)
	}
	for name := range Streams() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RunOperation runs the operation with that name. A name that is not one of ours, or arguments that the operation does
// not take, are a failure and nothing is run.
func RunOperation(env OpEnv, name string, args []string) Result {
	op, known := Operations()[name]
	if !known {
		return fail("unknown operation")
	}
	return op(env, args)
}

// reading is what one system gives for one operation: the number as text, or false when this system cannot say.
type reading func(sys StatsSystem) (string, bool)

// noArgs makes an operation of a measurement: it takes no arguments and answers with the number, or with a failure
// that says the system cannot measure it (never with a made-up zero).
func noArgs(read reading) Operation {
	return func(env OpEnv, args []string) Result {
		if len(args) != 0 {
			return fail("this operation takes no arguments")
		}
		if value, found := read(env.StatsSystem); found {
			return ok(value)
		}
		return fail("not available on " + env.GOOS)
	}
}

func cpuTime(sys StatsSystem) (string, bool) {
	switch sys.GOOS {
	case "linux":
		jiffies, _, found := linuxCPU(sys)
		return fmt.Sprintf("%.0f", float64(jiffies)/100), found
	case "freebsd":
		ticks, _, found := freebsdCPU(sys)
		hz := freebsdStatHz(sys)
		return fmt.Sprintf("%.0f", float64(ticks)/float64(hz)), found && hz > 0
	}
	return "", false
}

func cpuUsage(sys StatsSystem) (string, bool) {
	var sample func(StatsSystem) (total, idle int64, found bool)
	switch sys.GOOS {
	case "linux":
		sample = linuxCPU
	case "freebsd":
		sample = freebsdCPU
	default:
		return "", false
	}

	total1, idle1, found1 := sample(sys)
	if sys.Sleep != nil {
		sys.Sleep(500 * time.Millisecond)
	}
	total2, idle2, found2 := sample(sys)
	if !found1 || !found2 {
		return "", false
	}
	if total2 <= total1 {
		return "0", true
	}
	return fmt.Sprintf("%.2f", (1-float64(idle2-idle1)/float64(total2-total1))*100), true
}

// linuxCPU reads the first line of /proc/stat: the time of every mode, and the idle part of it (idle and iowait).
func linuxCPU(sys StatsSystem) (total, idle int64, found bool) {
	data, err := sys.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[0] != "cpu" {
			continue
		}
		for i, f := range fields[1:8] {
			n := atoi64(f)
			total += n
			if i == 3 || i == 4 {
				idle += n
			}
		}
		return total, idle, true
	}
	return 0, 0, false
}

// freebsdCPU reads kern.cp_time: user, nice, system, interrupt and idle ticks.
func freebsdCPU(sys StatsSystem) (total, idle int64, found bool) {
	out, err := sys.Run("sysctl", "-n", "kern.cp_time")
	fields := strings.Fields(out)
	if err != nil || len(fields) < 5 {
		return 0, 0, false
	}
	for _, f := range fields[:5] {
		total += atoi64(f)
	}
	return total, atoi64(fields[4]), true
}

// freebsdStatHz is the rate of the ticks of kern.cp_time, from `stathz = 128` in kern.clockrate.
func freebsdStatHz(sys StatsSystem) int64 {
	out, err := sys.Run("sysctl", "-n", "kern.clockrate")
	if err != nil {
		return 0
	}
	_, after, found := strings.Cut(out, "stathz =")
	fields := strings.Fields(after)
	if !found || len(fields) == 0 {
		return 0
	}
	return atoi64(strings.TrimRight(fields[0], ",}"))
}

func diskIODuration(sys StatsSystem) (string, bool) {
	if sys.GOOS != "linux" {
		return "", false
	}
	data, err := sys.ReadFile("/proc/diskstats")
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%.3f", float64(sumDiskstats(string(data), 12))/1000), true
}

func diskIOReads(sys StatsSystem) (string, bool) { return diskCounter(sys, 3) }

func diskIOWrites(sys StatsSystem) (string, bool) { return diskCounter(sys, 7) }

func diskCounter(sys StatsSystem, column int) (string, bool) {
	if sys.GOOS != "linux" {
		return "", false
	}
	data, err := sys.ReadFile("/proc/diskstats")
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(sumDiskstats(string(data), column), 10), true
}

// sumDiskstats adds one column of /proc/diskstats over the whole block devices (sd, vd, xvd, nvme, hd), without
// their partitions, which would count the same I/O twice.
func sumDiskstats(text string, column int) int64 {
	var sum int64
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) <= column || !wholeDisk(fields[2]) {
			continue
		}
		sum += atoi64(fields[column])
	}
	return sum
}

func wholeDisk(name string) bool {
	for _, prefix := range []string{"sd", "vd", "xvd", "hd"} {
		if rest, found := strings.CutPrefix(name, prefix); found && rest != "" && allLetters(rest) {
			return true
		}
	}
	if rest, found := strings.CutPrefix(name, "nvme"); found {
		digits, namespace, split := strings.Cut(rest, "n")
		return split && allDigits(digits) && allDigits(namespace)
	}
	return false
}

func allLetters(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return s != ""
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func diskUsed(sys StatsSystem) (string, bool) {
	switch sys.GOOS {
	case "linux":
		return linuxDiskUsed(sys)
	case "freebsd":
		out, err := sys.Run("df", "-k")
		if err != nil {
			return "", false
		}
		var sum int64
		lines := strings.Split(out, "\n")
		for _, line := range lines[min(1, len(lines)):] {
			if fields := strings.Fields(line); len(fields) >= 6 {
				sum += atoi64(fields[2]) * 1024
			}
		}
		return strconv.FormatInt(sum, 10), true
	}
	return "", false
}

// linuxDiskUsed adds the space in use of every mounted file system that has a size, once for each device, as df does.
func linuxDiskUsed(sys StatsSystem) (string, bool) {
	mounts, err := sys.ReadFile("/proc/mounts")
	if err != nil || sys.Statfs == nil {
		return "", false
	}
	var sum int64
	seen := map[string]bool{}
	for _, line := range strings.Split(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || seen[fields[0]] {
			continue
		}
		total, free, err := sys.Statfs(unescapeMount(fields[1]))
		if err != nil || total == 0 || free > total {
			continue
		}
		seen[fields[0]] = true
		sum += int64(total - free)
	}
	return strconv.FormatInt(sum, 10), true
}

func memoryCached(sys StatsSystem) (string, bool) {
	if sys.GOOS != "linux" {
		return "", false
	}
	data, err := sys.ReadFile("/proc/meminfo")
	if err != nil {
		return "", false
	}
	cached := meminfo(string(data), "Cached")
	return strconv.FormatInt(cached, 10), cached > 0
}

func memoryUsed(sys StatsSystem) (string, bool) {
	switch sys.GOOS {
	case "linux":
		data, err := sys.ReadFile("/proc/meminfo")
		if err != nil {
			return "", false
		}
		total, available := meminfo(string(data), "MemTotal"), meminfo(string(data), "MemAvailable")
		if total == 0 || available == 0 || available > total {
			return "", false
		}
		return strconv.FormatInt(total-available, 10), true
	case "freebsd":
		stats, found := collectFreeBSDStats(sys)
		return strconv.FormatInt(stats.MemoryUsage, 10), found && stats.MemoryUsage > 0
	}
	return "", false
}

func networkConnections(sys StatsSystem) (string, bool) {
	switch sys.GOOS {
	case "linux":
		count, found := 0, false
		for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
			if data, err := sys.ReadFile(path); err == nil {
				found = true
				count += establishedIn(string(data))
			}
		}
		return strconv.Itoa(count), found
	case "freebsd":
		out, err := sys.Run("netstat", "-an", "-p", "tcp")
		if err != nil {
			return "", false
		}
		count := 0
		for _, line := range strings.Split(out, "\n") {
			if strings.HasSuffix(strings.TrimSpace(line), "ESTABLISHED") {
				count++
			}
		}
		return strconv.Itoa(count), true
	}
	return "", false
}

// establishedIn counts the connections in state 01 (ESTABLISHED) of /proc/net/tcp or tcp6.
func establishedIn(text string) int {
	count := 0
	lines := strings.Split(text, "\n")
	for _, line := range lines[min(1, len(lines)):] { // the first line is the header
		if fields := strings.Fields(line); len(fields) > 3 && fields[3] == "01" {
			count++
		}
	}
	return count
}

func networkReceived(sys StatsSystem) (string, bool) {
	in, _, found := networkBytes(sys)
	return strconv.FormatInt(in, 10), found
}

func networkTransmitted(sys StatsSystem) (string, bool) {
	_, out, found := networkBytes(sys)
	return strconv.FormatInt(out, 10), found
}

func networkBytes(sys StatsSystem) (in, out int64, found bool) {
	switch sys.GOOS {
	case "linux":
		data, err := sys.ReadFile("/proc/net/dev")
		if err != nil {
			return 0, 0, false
		}
		in, out = netTotals(string(data))
		return in, out, true
	case "freebsd":
		text, err := sys.Run("netstat", "-ibn")
		if err != nil {
			return 0, 0, false
		}
		in, out = parseFreeBSDNetstat(text)
		return in, out, true
	}
	return 0, 0, false
}

func uptime(sys StatsSystem) (string, bool) {
	switch sys.GOOS {
	case "linux":
		data, err := sys.ReadFile("/proc/uptime")
		fields := strings.Fields(string(data))
		if err != nil || len(fields) == 0 {
			return "", false
		}
		seconds, err := strconv.ParseFloat(fields[0], 64)
		return fmt.Sprintf("%.0f", seconds), err == nil
	case "freebsd":
		out, err := sys.Run("sysctl", "-n", "kern.boottime")
		boot, parsed := parseFreeBSDBoot(out)
		if err != nil || !parsed || sys.Now == nil {
			return "", false
		}
		return strconv.FormatInt(sys.Now().Unix()-boot, 10), true
	}
	return "", false
}
