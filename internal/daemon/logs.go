package daemon

import (
	"encoding/json"
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
	kind     string // "file" or "journal"
	target   string
	severity string
	match    *regexp.Regexp // nil without a filter
}

// parseLog checks the arguments of `<file|journal> <path or unit> [filter] [min_severity]`: the type, that the path or the unit is one the
// owner declared, and that the filter is an expression (RE2, which cannot take exponential time).
func parseLog(env OpEnv, args []string) (logSource, *Result) {
	if len(args) < 2 || len(args) > 4 {
		r := fail("Missing parameters.")
		return logSource{}, &r
	}
	source := logSource{kind: args[0], target: args[1]}

	if len(args) >= 3 && args[2] != "" {
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

	if len(args) == 4 && args[3] != "" {
		if source.kind != "journal" || len(args[3]) != 1 || args[3][0] < '0' || args[3][0] > '7' {
			r := fail("Invalid journal severity.")
			return logSource{}, &r
		}
		source.severity = args[3]
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

// accept says whether a line passes the filter and gives the line as it has to be sent on.
//
// A line of the journal is JSON whose MESSAGE is text, but journald writes MESSAGE as an array of bytes when it holds
// anything that is not printable (the color codes of a logger are enough) or is very long. Such a line used to be
// dropped, so a log that had nothing but colored lines answered "OK" with no lines at all. It is sent on with MESSAGE as
// text (a byte that is not valid UTF-8 becomes U+FFFD), so whoever reads it needs not know. Every other line is sent on
// exactly as it came.
func (l logSource) accept(line string) (string, bool) {
	if l.kind != "journal" {
		return line, l.match == nil || l.match.MatchString(line)
	}
	var entry map[string]json.RawMessage
	if json.Unmarshal([]byte(line), &entry) != nil {
		return line, false
	}
	var message string
	if raw, found := entry["MESSAGE"]; found && json.Unmarshal(raw, &message) != nil {
		bytes, valid := messageBytes(raw)
		if !valid {
			return line, false
		}
		message = strings.ToValidUTF8(string(bytes), "�")
		text, _ := json.Marshal(message)
		entry["MESSAGE"] = text
		if rewritten, err := json.Marshal(entry); err == nil {
			line = string(rewritten)
		}
	}
	return line, l.match == nil || l.match.MatchString(message)
}

// messageBytes reads a MESSAGE that journald wrote as an array of numbers from 0 to 255.
func messageBytes(raw json.RawMessage) ([]byte, bool) {
	var numbers []int
	if json.Unmarshal(raw, &numbers) != nil {
		return nil, false
	}
	bytes := make([]byte, len(numbers))
	for i, n := range numbers {
		if n < 0 || n > 255 {
			return nil, false
		}
		bytes[i] = byte(n)
	}
	return bytes, true
}

// logOperation is `log_recent_matching.sh <file|journal> <path or unit> [filter] [min_severity]`: the last lines, among the last 500,
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
		r := env.run(ExecSpec{Argv: source.journalArgs("-n", "500"), Timeout: logTimeout})
		if r.Err != nil || r.ExitCode != 0 {
			return fail("The journal cannot be read.")
		}
		window = splitLines(r.Output)
	}

	var kept []string
	for _, line := range window {
		if line, passes := source.accept(line); passes {
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

func (l logSource) journalArgs(mode ...string) []string {
	args := append([]string{"journalctl"}, mode...)
	args = append(args, "--no-pager", "--output=json", "--output-fields=MESSAGE,__REALTIME_TIMESTAMP,PRIORITY", "--unit="+l.target)
	if l.severity != "" {
		args = append(args, "--priority="+l.severity)
	}
	return args
}
