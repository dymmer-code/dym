package daemon

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	procStat1 = "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 50 0 50 350 50 0 0 0 0 0\n"
	procStat2 = "cpu  200 0 200 750 150 0 0 0 0 0\n" // +100 user, +100 system, +50 idle, +50 iowait: 200 of 300 busy
	procTCP   = "  sl  local_address rem_address   st tx_queue\n   0: 0100007F:0035 00000000:0000 0A 00000000:00000000\n" +
		"   1: 0100007F:1F90 0100007F:C350 01 00000000:00000000\n   2: 0100007F:C350 0100007F:1F90 01 00000000:00000000\n"
	procTCP6       = "  sl  local_address rem_address   st tx_queue\n   0: 00000000000000000000000001000000:0050 0000:0000 01 0:0\n"
	procDiskstats2 = "   8       0 sda 100 0 1 0 200 0 0 0 0 1500 0\n   8       1 sda1 90 0 1 0 190 0 0 0 0 1400 0\n" +
		" 259       0 nvme0n1 10 0 1 0 20 0 0 0 0 500 0\n 259       1 nvme0n1p1 5 0 1 0 8 0 0 0 0 400 0\n 253       0 dm-0 7 0 1 0 7 0 0 0 0 700 0\n"
)

// linuxOps is a Linux host whose /proc changes between two readings, as the CPU does while the operation waits.
func linuxOps() StatsSystem {
	sys := linuxStatsSystem()
	readings := 0
	files := map[string]string{"/proc/net/tcp": procTCP, "/proc/net/tcp6": procTCP6, "/proc/diskstats": procDiskstats2}
	base := sys.ReadFile
	sys.ReadFile = func(path string) ([]byte, error) {
		if path == "/proc/stat" {
			readings++
			if readings == 1 {
				return []byte(procStat1), nil
			}
			return []byte(procStat2), nil
		}
		if text, found := files[path]; found {
			return []byte(text), nil
		}
		return base(path)
	}
	sys.Sleep = func(time.Duration) {}
	return sys
}

func ask(sys StatsSystem, name string, args ...string) Result { return RunOperation(sys, name, args) }

func TestLinuxOperations(t *testing.T) {
	cases := map[string]string{
		"cpu_time_seconds_total.sh":       "10",         // (100+100+700+100) jiffies, 100 per second
		"memory_cached_bytes.sh":          "0",          // set below
		"memory_used_bytes.sh":            "1024000000", // (4046480-3046480) KiB
		"network_active_connections.sh":   "3",          // two in tcp, one in tcp6; the listener is not one
		"network_receive_bytes_total.sh":  "1500",       // loopback does not count
		"network_transmit_bytes_total.sh": "2700",       //
		"system_uptime_seconds.sh":        "86401",      // 86400.77 rounded, as the script did
		"disk_io_reads_total.sh":          "110",        // sda and nvme0n1: partitions and dm-0 are not whole disks
		"disk_io_writes_total.sh":         "220",        //
		"disk_io_duration_seconds.sh":     "2.000",      // 1500 ms + 500 ms
		"cpu_usage_percent.sh":            "66.67",      // 200 busy of 300
	}
	delete(cases, "memory_cached_bytes.sh") // the sample /proc/meminfo has no Cached line: covered below

	for name, want := range cases {
		got := ask(linuxOps(), name) // its own system: the CPU one changes between readings
		if got.Status != "OK" || got.Message != want {
			t.Errorf("%s: got %+v, want OK %s", name, got, want)
		}
	}
}

func TestLinuxMemoryCachedAndDiskUsed(t *testing.T) {
	sys := linuxOps()
	base := sys.ReadFile
	sys.ReadFile = func(path string) ([]byte, error) {
		if path == "/proc/meminfo" {
			return []byte(procMeminfo + "Cached:          2000 kB\n"), nil
		}
		return base(path)
	}
	if got := ask(sys, "memory_cached_bytes.sh"); got.Message != "2048000" {
		t.Errorf("cached %+v", got)
	}
	// / has 1000 KiB with 400 free (600 used), /srv/data has 4000 used, "/with space" is empty and the loop device is a
	// device mounted twice only once.
	if got := ask(sys, "disk_used_bytes.sh"); got.Status != "OK" || got.Message != "618400" {
		t.Errorf("disk used %+v", got)
	}
}

func TestAnOperationTakesNoArguments(t *testing.T) {
	sys := linuxOps()
	for name := range Operations() {
		if got := ask(sys, name, "; touch /tmp/x"); got.Status != "FAIL" || !strings.Contains(got.Message, "no arguments") {
			t.Errorf("%s accepted an argument: %+v", name, got)
		}
	}
}

func TestAnUnknownOperationIsRefusedAndNothingIsRun(t *testing.T) {
	ran := false
	sys := linuxOps()
	sys.Run = func(string, ...string) (string, error) { ran = true; return "", nil }

	for _, name := range []string{"", "rm -rf /", "../../bin/sh", "docker.sh", "info.sh", "CPU_USAGE_PERCENT.SH", "cpu_usage_percent"} {
		if got := ask(sys, name); got.Status != "FAIL" || got.Message != "unknown operation" {
			t.Errorf("%q: %+v", name, got)
		}
	}
	if ran {
		t.Error("a command was run for a name that is not an operation")
	}
}

func TestAnOperationThatTheSystemCannotMeasureFailsInsteadOfSayingZero(t *testing.T) {
	got := ask(StatsSystem{GOOS: "plan9"}, "cpu_usage_percent.sh")
	if got.Status != "FAIL" || !strings.Contains(got.Message, "not available on plan9") {
		t.Errorf("%+v", got)
	}

	sys := linuxOps()
	sys.ReadFile = func(string) ([]byte, error) { return nil, errors.New("no /proc") }
	if got := ask(sys, "memory_used_bytes.sh"); got.Status != "FAIL" {
		t.Errorf("without /proc: %+v", got)
	}
}

func freebsdOps() StatsSystem {
	sys := freebsdStatsSystem()
	reads := 0
	base := sys.Run
	sys.Run = func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "sysctl -n kern.cp_time":
			reads++
			if reads == 1 {
				return "100 0 100 5 700\n", nil
			}
			return "200 0 200 15 750\n", nil
		case "sysctl -n kern.clockrate":
			return "{ hz = 1000, tick = 1000, profhz = 8128, stathz = 128 }\n", nil
		case "netstat -an -p tcp":
			return "Proto Recv-Q Send-Q Local Address Foreign Address (state)\ntcp4 0 0 10.0.0.5.22 1.2.3.4.5555 ESTABLISHED\ntcp4 0 0 *.22 *.* LISTEN\ntcp6 0 0 ::1.53 ::1.4000 ESTABLISHED\n", nil
		}
		return base(name, args...)
	}
	sys.Sleep = func(time.Duration) {}
	return sys
}

func TestFreeBSDOperations(t *testing.T) {
	want := map[string]string{
		"cpu_usage_percent.sh":            "80.77", // total +260 ticks, idle +50
		"cpu_time_seconds_total.sh":       "7",     // 905 ticks at 128 per second
		"memory_used_bytes.sh":            "2445934592",
		"network_active_connections.sh":   "2",
		"network_receive_bytes_total.sh":  "500100",
		"network_transmit_bytes_total.sh": "300200",
		"system_uptime_seconds.sh":        "3600",
		"disk_used_bytes.sh":              "13568001024", // 13250001 KiB
	}

	for name, expected := range want {
		got := ask(freebsdOps(), name)
		if got.Status != "OK" || got.Message != expected {
			t.Errorf("%s: got %+v, want %s", name, got, expected)
		}
	}
}

func TestFreeBSDDoesNotInventWhatItCannotMeasure(t *testing.T) {
	sys := freebsdOps()
	for _, name := range []string{"disk_io_reads_total.sh", "disk_io_writes_total.sh", "disk_io_duration_seconds.sh", "memory_cached_bytes.sh"} {
		if got := ask(sys, name); got.Status != "FAIL" || !strings.Contains(got.Message, "not available on freebsd") {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestOperationNamesAreTheScriptsDymmerAsksFor(t *testing.T) {
	names := OperationNames()
	if !sort.StringsAreSorted(names) || len(names) != 12 {
		t.Errorf("names %v", names)
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".sh") {
			t.Errorf("%s is not named like a script of Dymmer", name)
		}
	}
	if !reflect.DeepEqual(names, OperationNames()) {
		t.Error("the list is not stable")
	}
}

func TestWholeDisk(t *testing.T) {
	for name, want := range map[string]bool{
		"sda": true, "vdb": true, "xvdc": true, "hda": true, "nvme0n1": true, "nvme12n3": true,
		"sda1": false, "vda2": false, "nvme0n1p1": false, "dm-0": false, "loop0": false, "sd": false, "md0": false,
	} {
		if got := wholeDisk(name); got != want {
			t.Errorf("wholeDisk(%q) = %v, want %v", name, got, want)
		}
	}
}
