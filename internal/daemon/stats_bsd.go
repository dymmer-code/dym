package daemon

import (
	"bufio"
	"strconv"
	"strings"
)

func collectFreeBSDStats(sys StatsSystem) (Stats, bool) {
	var s Stats
	got := 0
	sysctl := func(name string) string {
		out, err := sys.Run("sysctl", "-n", name)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(out)
	}

	if load, ok := parseFreeBSDLoad(sysctl("vm.loadavg")); ok {
		s.CPULoad = load
		got++
	}

	// In use = everything but the pages that are free or inactive (the kernel hands those back on demand).
	physmem, pageSize := atoi64(sysctl("hw.physmem")), atoi64(sysctl("hw.pagesize"))
	free, inactive := atoi64(sysctl("vm.stats.vm.v_free_count")), atoi64(sysctl("vm.stats.vm.v_inactive_count"))
	if physmem > 0 && pageSize > 0 && (free+inactive)*pageSize <= physmem {
		s.MemoryUsage = physmem - (free+inactive)*pageSize
		got++
	}

	if boot, ok := parseFreeBSDBoot(sysctl("kern.boottime")); ok && sys.Now != nil {
		if up := sys.Now().Unix() - boot; up >= 0 {
			s.Uptime = up
			got++
		}
	}

	if out, err := sys.Run("netstat", "-ibn"); err == nil {
		s.NetworkIn, s.NetworkOut = parseFreeBSDNetstat(out)
	}
	if out, err := sys.Run("df", "-k"); err == nil {
		s.Volumes = parseFreeBSDDf(out)
	}
	return s, got > 0
}

// parseFreeBSDLoad reads `{ 0.52 0.41 0.36 }` and gives the first number.
func parseFreeBSDLoad(text string) (float64, bool) {
	fields := strings.Fields(strings.Trim(strings.TrimSpace(text), "{}"))
	if len(fields) == 0 {
		return 0, false
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	return load, err == nil
}

// parseFreeBSDBoot reads `{ sec = 1760000000, usec = 12345 } Fri Oct 17 ...` and gives the seconds.
func parseFreeBSDBoot(text string) (int64, bool) {
	_, after, ok := strings.Cut(text, "sec =")
	if !ok {
		return 0, false
	}
	value, _, _ := strings.Cut(after, ",")
	sec, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return sec, err == nil
}

// parseFreeBSDNetstat adds the bytes in and out of every interface but loopback from `netstat -ibn`. Each interface
// has one `<Link#N>` row with the totals (the rows of its addresses repeat them and are skipped). The Address column
// may be empty, so the columns are counted from the end, as the header says.
func parseFreeBSDNetstat(text string) (in, out int64) {
	inFromEnd, outFromEnd := 0, 0
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "Name" {
			for i, name := range fields {
				switch name {
				case "Ibytes":
					inFromEnd = len(fields) - i
				case "Obytes":
					outFromEnd = len(fields) - i
				}
			}
			continue
		}
		if inFromEnd == 0 || outFromEnd == 0 || len(fields) < 4 || !strings.HasPrefix(fields[2], "<Link#") {
			continue
		}
		if strings.HasPrefix(fields[0], "lo") || len(fields) < max(inFromEnd, outFromEnd) {
			continue
		}
		in += atoi64(fields[len(fields)-inFromEnd])
		out += atoi64(fields[len(fields)-outFromEnd])
	}
	return in, out
}

// parseFreeBSDDf reads `df -k`: the block devices, and the ZFS dataset that is the root (its device is not under
// /dev). FreeBSD keeps no per-device I/O counters that are cheap to read, so reads and writes are left out.
func parseFreeBSDDf(text string) []VolumeUsage {
	var out []VolumeUsage
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Scan() // the header
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			continue
		}
		device, mountpoint := fields[0], strings.Join(fields[5:], " ")
		if !strings.HasPrefix(device, "/dev/") && mountpoint != "/" {
			continue
		}
		if seen[device] {
			continue
		}
		seen[device] = true
		out = append(out, VolumeUsage{Device: device, Size: atoi64(fields[1]) * 1024, Used: atoi64(fields[2]) * 1024})
	}
	return out
}
