package daemon

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

const (
	procMeminfo = "MemTotal:        4046480 kB\nMemFree:          412000 kB\nMemAvailable:    3046480 kB\nBuffers: 1 kB\n"
	procNetDev  = "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo: 9000      10    0    0    0     0          0         0     9000      10    0    0    0     0       0          0\n" +
		"  eth0: 1000      10    0    0    0     0          0         0     2000      10    0    0    0     0       0          0\n" +
		"  eth1:  500       5    0    0    0     0          0         0      700       5    0    0    0     0       0          0\n"
	procMounts = "sysfs /sys sysfs rw 0 0\n/dev/sda1 / ext4 rw 0 0\n/dev/sda1 /mnt/again ext4 rw 0 0\n" +
		"/dev/mapper/vg-data /srv/data ext4 rw 0 0\n/dev/loop3 /snap/core snap ro 0 0\n/dev/sdb1 /with\\040space ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n"
	procDiskstats = "   8       0 sda 1 0 1 0 0 0 0 0\n   8       1 sda1 500 0 0 0 700 0 0 0 0 0\n 253       0 dm-0 90 0 0 0 80 0 0 0 0 0\n"
)

func linuxStatsSystem() StatsSystem {
	files := map[string]string{
		"/proc/loadavg":   "0.52 0.41 0.36 1/200 12345\n",
		"/proc/meminfo":   procMeminfo,
		"/proc/uptime":    "86400.77 170000.10\n",
		"/proc/net/dev":   procNetDev,
		"/proc/mounts":    procMounts,
		"/proc/diskstats": procDiskstats,
	}
	return StatsSystem{
		GOOS: "linux",
		ReadFile: func(path string) ([]byte, error) {
			if text, ok := files[path]; ok {
				return []byte(text), nil
			}
			return nil, errors.New("no such file")
		},
		Statfs: func(path string) (uint64, uint64, error) {
			switch path {
			case "/":
				return 1000 * 1024, 400 * 1024, nil
			case "/srv/data":
				return 5000, 1000, nil
			case "/with space":
				return 100, 100, nil
			}
			return 0, 0, errors.New("not mounted")
		},
		ResolveDevice: func(device string) string {
			if device == "/dev/mapper/vg-data" {
				return "/dev/dm-0"
			}
			return device
		},
	}
}

func TestCollectStatsLinux(t *testing.T) {
	got, ok := CollectStats(linuxStatsSystem())
	if !ok {
		t.Fatal("a Linux host must give a sample")
	}

	if got.CPULoad != 0.52 {
		t.Errorf("load %v", got.CPULoad)
	}
	if want := int64((4046480 - 3046480) * 1024); got.MemoryUsage != want {
		t.Errorf("memory %d, want %d", got.MemoryUsage, want)
	}
	if got.Uptime != 86400 {
		t.Errorf("uptime %d", got.Uptime)
	}
	if got.NetworkIn != 1500 || got.NetworkOut != 2700 {
		t.Errorf("network in/out %d/%d: loopback must not count", got.NetworkIn, got.NetworkOut)
	}

	if len(got.Volumes) != 3 {
		t.Fatalf("volumes %+v: loop devices and tmpfs go, a device mounted twice counts once", got.Volumes)
	}
	root := got.Volumes[0]
	if root.Device != "/dev/sda1" || root.Size != 1000*1024 || root.Used != 600*1024 || root.Reads == nil || *root.Reads != 500 || *root.Writes != 700 {
		t.Errorf("root volume %+v", root)
	}
	data := got.Volumes[1]
	if data.Device != "/dev/mapper/vg-data" || data.Used != 4000 || data.Reads == nil || *data.Reads != 90 {
		t.Errorf("a mapper device is counted under its dm-N name: %+v", data)
	}
	if got.Volumes[2].Device != "/dev/sdb1" || got.Volumes[2].Used != 0 {
		t.Errorf("a mount point with an escaped space is found: %+v", got.Volumes[2])
	}
}

func TestStatsPayloadLeavesOutWhatIsNotKnown(t *testing.T) {
	reads := int64(5)
	payload := Stats{CPULoad: 1.5, Volumes: []VolumeUsage{{Device: "/dev/a", Used: 10, Reads: &reads}}}.Payload()

	volume := payload["volumes"].([]map[string]any)[0]
	if volume["reads"] != int64(5) {
		t.Errorf("reads %v", volume["reads"])
	}
	if _, present := volume["writes"]; present {
		t.Error("writes were not known: they must not be sent as zero")
	}
	if payload["cpu_load"] != 1.5 {
		t.Errorf("load %v", payload["cpu_load"])
	}
}

func TestCollectStatsGivesNothingWithoutTheBasics(t *testing.T) {
	sys := linuxStatsSystem()
	sys.ReadFile = func(string) ([]byte, error) { return nil, errors.New("no /proc") }
	if _, ok := CollectStats(sys); ok {
		t.Error("with nothing readable there is no sample")
	}
	if _, ok := CollectStats(StatsSystem{GOOS: "plan9"}); ok {
		t.Error("a system that is not supported gives no sample")
	}
}

const freebsdNetstat = "Name    Mtu Network       Address              Ipkts Ierrs Idrop     Ibytes    Opkts Oerrs     Obytes  Coll\n" +
	"vtnet0 1500 <Link#1>      aa:bb:cc:dd:ee:ff    1000     0     0     500000      900     0     300000     0\n" +
	"vtnet0    - 10.0.0.0/24   10.0.0.5              900     -     -     400000      800     -     250000     -\n" +
	"lo0   16384 <Link#2>                              50     0     0       9000       50     0       9000     0\n" +
	"epair0a 1500 <Link#3>                              5     0     0        100        6     0        200     0\n"

const freebsdDf = "Filesystem         1024-blocks     Used    Avail Capacity  Mounted on\n" +
	"zroot/ROOT/default    50000000 10000000 40000000    20%    /\n" +
	"devfs                        1        1        0   100%    /dev\n" +
	"/dev/ada1p1           1000000   250000   750000    25%    /data disk\n" +
	"zroot/usr/home        50000000  3000000 40000000     7%    /usr/home\n"

func freebsdStatsSystem() StatsSystem {
	outputs := map[string]string{
		"sysctl -n vm.loadavg":                   "{ 0.52 0.41 0.36 }\n",
		"sysctl -n hw.physmem":                   "8589934592\n",
		"sysctl -n hw.pagesize":                  "4096\n",
		"sysctl -n vm.stats.vm.v_free_count":     "1000000\n",
		"sysctl -n vm.stats.vm.v_inactive_count": "500000\n",
		"sysctl -n kern.boottime":                "{ sec = 1760000000, usec = 123456 } Thu Oct  9 08:53:20 2025\n",
		"netstat -ibn":                           freebsdNetstat,
		"df -k":                                  freebsdDf,
	}
	return StatsSystem{
		GOOS: "freebsd",
		Now:  func() time.Time { return time.Unix(1760000000+3600, 0) },
		Run: func(name string, args ...string) (string, error) {
			key := name
			for _, a := range args {
				key += " " + a
			}
			if out, ok := outputs[key]; ok {
				return out, nil
			}
			return "", errors.New("unexpected command " + key)
		},
	}
}

func TestCollectStatsFreeBSD(t *testing.T) {
	got, ok := CollectStats(freebsdStatsSystem())
	if !ok {
		t.Fatal("a FreeBSD host must give a sample")
	}
	if got.CPULoad != 0.52 {
		t.Errorf("load %v", got.CPULoad)
	}
	if want := int64(8589934592 - 1500000*4096); got.MemoryUsage != want {
		t.Errorf("memory %d, want %d", got.MemoryUsage, want)
	}
	if got.Uptime != 3600 {
		t.Errorf("uptime %d", got.Uptime)
	}
	// The row of the address repeats the interface and loopback does not count: vtnet0 and epair0a.
	if got.NetworkIn != 500100 || got.NetworkOut != 300200 {
		t.Errorf("network in/out %d/%d", got.NetworkIn, got.NetworkOut)
	}

	want := []VolumeUsage{
		{Device: "zroot/ROOT/default", Size: 50000000 * 1024, Used: 10000000 * 1024},
		{Device: "/dev/ada1p1", Size: 1000000 * 1024, Used: 250000 * 1024},
	}
	if !reflect.DeepEqual(got.Volumes, want) {
		t.Errorf("volumes %+v, want %+v", got.Volumes, want)
	}
}

func TestFreeBSDNetstatWithAnEmptyAddressColumn(t *testing.T) {
	// A row without Address has one column less; the columns are counted from the end.
	in, out := parseFreeBSDNetstat(freebsdNetstat)
	if in != 500100 || out != 300200 {
		t.Errorf("%d/%d", in, out)
	}
}

func TestParseFreeBSDBootAndLoad(t *testing.T) {
	if sec, ok := parseFreeBSDBoot("{ sec = 1760000000, usec = 5 } Thu"); !ok || sec != 1760000000 {
		t.Errorf("boot %d %v", sec, ok)
	}
	if _, ok := parseFreeBSDBoot("garbage"); ok {
		t.Error("garbage is not a boot time")
	}
	if load, ok := parseFreeBSDLoad("{ 2.50 1 1 }"); !ok || load != 2.5 {
		t.Errorf("load %v %v", load, ok)
	}
}
