//go:build !windows

package daemon

import (
	"context"
	"os"
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

func envOf(t *testing.T, spec ExecSpec) map[string]string {
	t.Helper()
	spec.Argv = []string{"env"}
	out := map[string]string{}
	for _, line := range strings.Split(HostExec(context.Background(), spec).Output, "\n") {
		if k, v, found := strings.Cut(line, "="); found {
			out[k] = v
		}
	}
	return out
}

func TestACommandGetsTheHomeOfTheUserThatRunsTheDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := envOf(t, ExecSpec{})["HOME"]; got != home {
		t.Errorf("HOME is %q, want %q: a script of the user could not find its files", got, home)
	}
}

func TestACommandGetsTheTemporaryDirectoryWhenTheUserHasNoHome(t *testing.T) {
	t.Setenv("HOME", "/nonexistent-home-of-a-service-user")
	if got := envOf(t, ExecSpec{})["HOME"]; got != os.TempDir() {
		t.Errorf("HOME is %q, want %q", got, os.TempDir())
	}
}

func TestOnlyTheVariablesThatAreListedPassFromTheEnvironmentOfTheDaemon(t *testing.T) {
	t.Setenv("USER", "ignacio")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("DOCKER_HOST", "unix:///run/user/1000/docker.sock")
	t.Setenv("DYM_TEST_SECRET", "hunter2")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "k")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent")

	env := envOf(t, ExecSpec{})
	for name, want := range map[string]string{"USER": "ignacio", "XDG_RUNTIME_DIR": "/run/user/1000", "DOCKER_HOST": "unix:///run/user/1000/docker.sock"} {
		if env[name] != want {
			t.Errorf("%s is %q, want %q", name, env[name], want)
		}
	}
	for _, name := range []string{"DYM_TEST_SECRET", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK"} {
		if _, found := env[name]; found {
			t.Errorf("%s was passed on", name)
		}
	}
}

func TestWhatTheOwnerWritesForACommandWinsOverWhatIsInherited(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DOCKER_HOST", "unix:///inherited.sock")
	env := envOf(t, ExecSpec{Env: map[string]string{"HOME": "/srv/app-home", "DOCKER_HOST": "unix:///written.sock", "MODE": "prod"}})
	if env["HOME"] != "/srv/app-home" || env["DOCKER_HOST"] != "unix:///written.sock" || env["MODE"] != "prod" {
		t.Errorf("environment: %v", env)
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
