//go:build !windows

package daemon

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHostExecDoesNotInterpretAnything(t *testing.T) {
	r := HostExec(context.Background(), ExecSpec{Argv: []string{"echo", "a;b", "$(id)", "`id`", "*", "&&", "x"}})
	if r.Err != nil || r.ExitCode != 0 || r.Output != "a;b $(id) `id` * && x" {
		t.Errorf("%+v", r)
	}
}

func TestHostExecSaysHowItEnded(t *testing.T) {
	if r := HostExec(context.Background(), ExecSpec{Argv: []string{"false"}}); r.ExitCode != 1 || r.Err != nil {
		t.Errorf("false: %+v", r)
	}
	r := HostExec(context.Background(), ExecSpec{Argv: []string{"/nonexistent/program"}})
	if r.ExitCode != -1 || r.Err == nil {
		t.Errorf("not found: %+v", r)
	}
	if r := HostExec(context.Background(), ExecSpec{}); r.Err == nil {
		t.Error("no command is an error")
	}
}

func TestHostExecStopsWhatTakesTooLong(t *testing.T) {
	started := time.Now()
	r := HostExec(context.Background(), ExecSpec{Argv: []string{"sleep", "30"}, Timeout: 150 * time.Millisecond})
	if r.Err == nil || time.Since(started) > 5*time.Second {
		t.Errorf("it did not stop in time: %+v after %s", r, time.Since(started))
	}
}

func TestHostExecEndsTheChildrenOfWhatTimedOut(t *testing.T) {
	// The shell starts a child and waits for it: killing only the shell would leave `sleep` running and the output open.
	started := time.Now()
	r := HostExec(context.Background(), ExecSpec{Argv: []string{"sh", "-c", "sleep 30 & wait"}, Timeout: 150 * time.Millisecond})
	if r.Err == nil || time.Since(started) > 5*time.Second {
		t.Errorf("the group was not ended: %+v after %s", r, time.Since(started))
	}
}

func TestHostExecKeepsOnlyTheEndOfALongOutput(t *testing.T) {
	r := HostExec(context.Background(), ExecSpec{Argv: []string{"sh", "-c", "head -c 200000 /dev/zero | tr '\\0' a; echo END"}})
	if len(r.Output) > maxOutput || !strings.HasSuffix(r.Output, "aEND") {
		t.Errorf("kept %d bytes, ends with %q", len(r.Output), r.Output[max(len(r.Output)-6, 0):])
	}
}

func TestHostExecGivesACleanEnvironmentAndTheDirectory(t *testing.T) {
	t.Setenv("DYM_TEST_SECRET", "hunter2")
	r := HostExec(context.Background(), ExecSpec{Argv: []string{"env"}, Env: map[string]string{"MODE": "prod"}})
	if strings.Contains(r.Output, "hunter2") || !strings.Contains(r.Output, "MODE=prod") || !strings.Contains(r.Output, "PATH=/usr/local/sbin") {
		t.Errorf("environment:\n%s", r.Output)
	}

	dir := t.TempDir()
	r = HostExec(context.Background(), ExecSpec{Argv: []string{"pwd", "-P"}, Dir: dir})
	if !strings.HasSuffix(r.Output, strings.TrimPrefix(dir, "/private")) {
		t.Errorf("pwd %q in %q", r.Output, dir)
	}
}
