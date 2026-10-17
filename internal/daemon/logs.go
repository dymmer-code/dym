package daemon

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	logWindow  = 500     // lines looked at
	logKeep    = 15      // matching lines returned
	logBytes   = 1 << 20 // the most that is read from the end of a file to find those lines
	maxFilter  = 256     // characters of a filter
	logTimeout = 15 * time.Second
)

// logSource is what a log operation was asked to read, once it was checked against the configuration of the owner.
type logSource struct {
	kind   string // "file" or "journal"
	target string
	match  *regexp.Regexp // nil without a filter
}

// parseLog checks the arguments of `<file|journal> <path or unit> [filter]`: the type, that the path or the unit is one the
// owner declared, and that the filter is an expression (RE2, which cannot take exponential time).
func parseLog(env OpEnv, args []string) (logSource, *Result) {
	if len(args) < 2 || len(args) > 3 {
		r := fail("Missing parameters.")
		return logSource{}, &r
	}
	source := logSource{kind: args[0], target: args[1]}

	if len(args) == 3 && args[2] != "" {
		if len(args[2]) > maxFilter {
			r := fail("The filter is too long.")
			return logSource{}, &r
		}
		match, err := regexp.Compile(args[2])
		if err != nil {
			r := fail("The filter is not a valid expression.")
			return logSource{}, &r
		}
		source.match = match
	}

	config, err := env.config()
	if err != nil {
		r := fail("The configuration of this host cannot be read: " + err.Error())
		return logSource{}, &r
	}

	switch source.kind {
	case "file":
		if !includes(config.Logs.Files, source.target) {
			r := fail("The configuration of this host does not declare that file to be read.")
			return logSource{}, &r
		}
	case "journal":
		if !includes(config.Logs.Units, source.target) {
			r := fail("The configuration of this host does not declare that unit to be read.")
			return logSource{}, &r
		}
	default:
		r := fail("Only a file or the journal can be read.")
		return logSource{}, &r
	}
	return source, nil
}

func (l logSource) matches(line string) bool { return l.match == nil || l.match.MatchString(line) }

// logOperation is `log_recent_matching.sh <file|journal> <path or unit> [filter]`: the last lines, among the last 500,
// that match the filter. Only the files and the units that the owner declared can be read, whatever is asked.
func logOperation(env OpEnv, args []string) Result {
	source, refused := parseLog(env, args)
	if refused != nil {
		return *refused
	}

	var window []string
	if source.kind == "file" {
		lines, readable := tailFile(source.target)
		if !readable {
			return fail("The file cannot be read.")
		}
		window = lines
	} else {
		r := env.run(ExecSpec{Argv: []string{"journalctl", "-n", "500", "--no-pager", "--output=cat", "--unit=" + source.target}, Timeout: logTimeout})
		if r.Err != nil || r.ExitCode != 0 {
			return fail("The journal cannot be read.")
		}
		window = splitLines(r.Output)
	}

	var kept []string
	for _, line := range window {
		if source.matches(line) {
			kept = append(kept, line)
		}
	}
	if len(kept) > logKeep {
		kept = kept[len(kept)-logKeep:]
	}
	return ok(strings.Join(kept, "\n"))
}

// tailFile gives the last lines of a file, reading only the end of it. A path that is a link to somewhere else is read
// where it points, as `tail` does; what is declared is the path.
func tailFile(path string) ([]string, bool) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, false
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	start := max(info.Size()-logBytes, 0)
	buf := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && len(buf) > 0 {
		return nil, false
	}
	lines := splitLines(string(buf))
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first one starts before what was read
	}
	return lines[max(len(lines)-logWindow, 0):], true
}

func splitLines(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
