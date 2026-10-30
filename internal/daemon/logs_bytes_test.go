package daemon

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// journalBytes is a line of `journalctl --output=json` whose MESSAGE journald wrote as an array of bytes, which is what
// it does when the message holds a color code of a logger (or is very long).
func journalBytes(message string, raw ...int) string {
	numbers := raw
	if message != "" {
		numbers = nil
		for _, b := range []byte(message) {
			numbers = append(numbers, int(b))
		}
	}
	encoded, _ := json.Marshal(numbers)
	return `{"PRIORITY":"4","__REALTIME_TIMESTAMP":"1793329367000000","MESSAGE":` + string(encoded) + `}`
}

func journalMessage(t *testing.T, line string) string {
	t.Helper()
	var entry struct {
		Message string `json:"MESSAGE"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		t.Fatalf("not a line with a text MESSAGE: %v: %s", err, line)
	}
	return entry.Message
}

func recentJournal(t *testing.T, output string, filter string) Result {
	t.Helper()
	config := Config{Logs: LogsConfig{Units: []string{"myapp"}}}
	command := "journalctl -n 500 --no-pager --output=json --output-fields=MESSAGE,__REALTIME_TIMESTAMP,PRIORITY --unit=myapp"
	env, _ := envWith(config, map[string]ExecResult{command: okResult(output)})
	return RunOperation(env, "log_recent_matching.sh", []string{"journal", "myapp", filter})
}

// A log with nothing but colored lines used to answer OK with no lines at all; now it answers with them, in plain text.
func TestJournalLinesWithByteArrayMessageAreKept(t *testing.T) {
	colored := "2026-10-29T08:52:44.004Z \x1b[33mwarn\x1b[39m: - null GET /.env 404 0.2 -"
	r := recentJournal(t, journalBytes(colored)+"\n"+journalBytes("2026-10-29T08:52:45.000Z \x1b[32minfo\x1b[39m: started"), "")
	if r.Status != "OK" {
		t.Fatalf("%+v", r)
	}
	lines := strings.Split(r.Message, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected the 2 lines, got %d: %q", len(lines), r.Message)
	}
	// The color codes are not sent on: nobody reads them in a log.
	if got, want := journalMessage(t, lines[0]), "2026-10-29T08:52:44.004Z warn: - null GET /.env 404 0.2 -"; got != want {
		t.Fatalf("the message is %q, want %q", got, want)
	}
	var entry map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &entry)
	if entry["PRIORITY"] != "4" || entry["__REALTIME_TIMESTAMP"] != "1793329367000000" {
		t.Fatalf("the other fields were lost: %v", entry)
	}
}

func TestJournalFilterIsAppliedToTheTextOfAByteArrayMessage(t *testing.T) {
	output := journalBytes("\x1b[33mwarn\x1b[39m: first") + "\n" + journalBytes("\x1b[32minfo\x1b[39m: second") + "\n" + `{"MESSAGE":"warn: plain"}`
	r := recentJournal(t, output, "warn")
	if r.Status != "OK" {
		t.Fatalf("%+v", r)
	}
	lines := strings.Split(r.Message, "\n")
	if len(lines) != 2 || !strings.Contains(journalMessage(t, lines[0]), "first") || lines[1] != `{"MESSAGE":"warn: plain"}` {
		t.Fatalf("unexpected: %q", r.Message)
	}
}

func TestJournalLinesWithTextMessageAreSentOnUnchanged(t *testing.T) {
	line := `{"MESSAGE":"keep <this> & that","PRIORITY":"6"}`
	if r := recentJournal(t, line, ""); r.Message != line {
		t.Fatalf("changed: %q", r.Message)
	}
}

func TestJournalByteArrayThatIsNotUTF8IsRepaired(t *testing.T) {
	r := recentJournal(t, journalBytes("", 'a', 0xff, 'b'), "")
	if got := journalMessage(t, r.Message); got != "a�b" {
		t.Fatalf("%q", got)
	}
}

func TestJournalByteArrayWithAnImpossibleByteIsDropped(t *testing.T) {
	r := recentJournal(t, journalBytes("", 'a', 300)+"\n"+journalBytes("", 'o', 'k'), "")
	if r.Status != "OK" || journalMessage(t, r.Message) != "ok" {
		t.Fatalf("%+v", r)
	}
}

func TestJournalTextMessageLosesItsColorCodesToo(t *testing.T) {
	line := `{"MESSAGE":"\u001b[33mwarn\u001b[39m: plain","PRIORITY":"4"}`
	r := recentJournal(t, line, "^warn")
	if got := journalMessage(t, r.Message); got != "warn: plain" {
		t.Fatalf("%q (%+v)", got, r)
	}
}

func TestStripANSI(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                                    "plain",
		"\x1b[33mwarn\x1b[39m: x":                  "warn: x",
		"\x1b[1;31;40mbold\x1b[0m":                 "bold",
		"\x1b[2K\x1b[1Gline":                       "line",
		"\x1b]0;title\x07text":                     "text",
		"\x1b]8;;http://a\x1b\\link\x1b]8;;\x1b\\": "link",
		"50% [done] \x1b[0m":                       "50% [done] ",
	} {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogFileLinesLoseTheirColorCodes(t *testing.T) {
	source := logSource{kind: "file", match: regexp.MustCompile("^warn")}
	line, passes := source.accept("\x1b[33mwarn\x1b[39m: from a file")
	if !passes || line != "warn: from a file" {
		t.Fatalf("%q %v", line, passes)
	}
}
