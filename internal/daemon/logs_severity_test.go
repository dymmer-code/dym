package daemon

import (
	"strings"
	"testing"
)

func TestJournalSeverity(t *testing.T) {
	config := Config{Logs: LogsConfig{Units: []string{"nginx"}, Files: []string{"/tmp/log"}}}
	for _, severity := range []string{"0", "4", "7"} {
		command := "journalctl -n 500 --no-pager --output=json --output-fields=MESSAGE,__REALTIME_TIMESTAMP,PRIORITY --unit=nginx --priority=" + severity
		env, _ := envWith(config, map[string]ExecResult{command: okResult(`{"MESSAGE":"keep"}` + "\n" + `{"MESSAGE":"skip"}`)})
		r := RunOperation(env, "log_recent_matching.sh", []string{"journal", "nginx", "keep", severity})
		if r.Status != "OK" || r.Message != `{"MESSAGE":"keep"}` {
			t.Fatalf("severity %s: %+v", severity, r)
		}
	}
	env, f := envWith(config, nil)
	for _, args := range [][]string{{"journal", "nginx", "", "8"}, {"journal", "nginx", "", "-1"}, {"journal", "nginx", "", "4;id"}, {"file", "/tmp/log", "", "4"}} {
		if r := RunOperation(env, "log_recent_matching.sh", args); r.Status != "FAIL" {
			t.Fatalf("accepted %v", args)
		}
	}
	if len(f.lines()) != 0 {
		t.Fatal(strings.Join(f.lines(), "\n"))
	}
}
