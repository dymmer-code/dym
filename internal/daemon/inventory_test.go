package daemon

import (
	"errors"
	"net"
	"reflect"
	"testing"
)

func TestParseOSRelease(t *testing.T) {
	name, version := ParseOSRelease("PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"13\"\nID=debian\n")
	if name != "debian" || version != "13" {
		t.Errorf("got %q %q", name, version)
	}

	name, version = ParseOSRelease("garbage\nID='alpine'\n")
	if name != "alpine" || version != "" {
		t.Errorf("got %q %q", name, version)
	}
}

func TestParseCPUModel(t *testing.T) {
	x86 := "processor\t: 0\nvendor_id\t: AuthenticAMD\nmodel name\t: AMD Ryzen 9 5950X 16-Core Processor\ncpu MHz\t\t: 3400\n"
	if got := ParseCPUModel(x86); got != "AMD Ryzen 9 5950X 16-Core Processor" {
		t.Errorf("x86: %q", got)
	}
	if got := ParseCPUModel("Processor\t: ARMv7 Processor rev 3 (v7l)\n"); got != "ARMv7 Processor rev 3 (v7l)" {
		t.Errorf("arm: %q", got)
	}
	if got := ParseCPUModel("nothing here"); got != "" {
		t.Errorf("unknown: %q", got)
	}
}

func TestParseMemTotalGivesBytesFromKiB(t *testing.T) {
	text := "MemTotal:        8388608 kB\nMemFree:         1000 kB\n"
	if got := ParseMemTotal(text); got != 8388608*1024 {
		t.Errorf("got %d", got)
	}
	if ParseMemTotal("MemFree: 5 kB\n") != 0 || ParseMemTotal("MemTotal:\n") != 0 {
		t.Error("a missing or empty MemTotal must give 0")
	}
}

func ipNet(cidr string) net.Addr {
	ip, n, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	n.IP = ip
	return n
}

func TestAddressesLeaveOutWhatDoesNotSayWhereTheHostIs(t *testing.T) {
	sys := System{Addrs: func() ([]net.Addr, error) {
		return []net.Addr{
			ipNet("127.0.0.1/8"), ipNet("::1/128"), ipNet("fe80::1/64"), ipNet("224.0.0.1/32"),
			ipNet("192.168.1.57/24"), ipNet("2a01:4f8:161:eb::5/64"), ipNet("192.168.1.57/24"),
			&net.UnixAddr{Name: "x", Net: "unix"},
		}, nil
	}}

	want := []string{"192.168.1.57", "2a01:4f8:161:eb::5"}
	if got := Addresses(sys); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}

	sys.Addrs = func() ([]net.Addr, error) { return nil, errors.New("no interfaces") }
	if got := Addresses(sys); got != nil {
		t.Errorf("an error gives nothing, got %v", got)
	}
}

func linuxSystem(files map[string]string) System {
	return System{
		GOOS: "linux", GOARCH: "amd64", NumCPU: 4,
		Hostname: func() (string, error) { return "rowan", nil },
		Addrs:    func() ([]net.Addr, error) { return []net.Addr{ipNet("192.168.1.57/24")}, nil },
		ReadFile: func(path string) ([]byte, error) {
			if text, ok := files[path]; ok {
				return []byte(text), nil
			}
			return nil, errors.New("not found")
		},
	}
}

func TestCollectOnLinuxWritesTheCPUAsTheRestOfDymmerDoes(t *testing.T) {
	inv := Collect(linuxSystem(map[string]string{
		"/etc/os-release": "ID=debian\nVERSION_ID=\"13\"\n",
		"/proc/cpuinfo":   "model name\t: AMD Ryzen\n",
		"/proc/meminfo":   "MemTotal: 2048 kB\n",
	}))

	want := Inventory{
		Hostname: "rowan", IPAddresses: []string{"192.168.1.57"}, OSName: "debian", OSVersion: "13",
		CPUName: "x86_64 - AMD Ryzen", CPUCores: 4, Memory: 2048 * 1024,
	}
	if !reflect.DeepEqual(inv, want) {
		t.Errorf("got %+v\nwant %+v", inv, want)
	}
}

func TestCollectKeepsWhatItCanReadWhenSomethingIsMissing(t *testing.T) {
	inv := Collect(linuxSystem(nil))

	if inv.OSName != "linux" || inv.Hostname != "rowan" || inv.CPUCores != 4 || inv.Memory != 0 || inv.CPUName != "" {
		t.Errorf("got %+v", inv)
	}
}

func TestCollectOnOtherSystemsUsesSysctl(t *testing.T) {
	values := map[string]string{
		"kern.osproductversion": "15.1", "machdep.cpu.brand_string": "Apple M3", "hw.memsize": "17179869184",
		"kern.osrelease": "14.1-RELEASE", "hw.model": "Intel Xeon", "hw.physmem": "8589934592",
	}
	sysctl := func(name string) (string, error) {
		if v, ok := values[name]; ok {
			return v, nil
		}
		return "", errors.New("unknown")
	}
	base := System{NumCPU: 8, Hostname: func() (string, error) { return "h", nil }, Addrs: func() ([]net.Addr, error) { return nil, nil }, Sysctl: sysctl}

	mac := base
	mac.GOOS, mac.GOARCH = "darwin", "arm64"
	if got := Collect(mac); got.OSName != "macos" || got.OSVersion != "15.1" || got.CPUName != "aarch64 - Apple M3" || got.Memory != 17179869184 {
		t.Errorf("darwin: %+v", got)
	}

	bsd := base
	bsd.GOOS, bsd.GOARCH = "freebsd", "amd64"
	if got := Collect(bsd); got.OSName != "freebsd" || got.OSVersion != "14.1-RELEASE" || got.CPUName != "x86_64 - Intel Xeon" || got.Memory != 8589934592 {
		t.Errorf("freebsd: %+v", got)
	}

	other := base
	other.GOOS, other.GOARCH = "plan9", "amd64"
	if got := Collect(other); got.OSName != "plan9" {
		t.Errorf("other: %+v", got)
	}
}

func TestPayloadHasOnlyWhatIsKnownAndTheNamesDymmerStores(t *testing.T) {
	full := Inventory{Hostname: "h", IPAddresses: []string{"10.0.0.1"}, OSName: "debian", OSVersion: "13", CPUName: "c", CPUCores: 2, Memory: 5}.Payload()
	for _, key := range []string{"hostname", "ip_addresses", "os_name", "os_version", "cpu_name", "cpu_cores", "memory"} {
		if _, ok := full[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	if len(Inventory{}.Payload()) != 0 {
		t.Error("an empty inventory has an empty payload")
	}
}
