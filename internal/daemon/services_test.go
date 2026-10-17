package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeExec answers the commands by their line, and remembers what was asked.
type fakeExec struct {
	mu      sync.Mutex
	answers map[string]ExecResult
	ran     []ExecSpec
}

func (f *fakeExec) exec(_ context.Context, spec ExecSpec) ExecResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, spec)
	if r, found := f.answers[strings.Join(spec.Argv, " ")]; found {
		return r
	}
	return ExecResult{ExitCode: 1, Output: "unexpected: " + strings.Join(spec.Argv, " ")}
}

func (f *fakeExec) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.ran {
		out = append(out, strings.Join(s.Argv, " "))
	}
	return out
}

func envWith(config Config, answers map[string]ExecResult) (OpEnv, *fakeExec) {
	f := &fakeExec{answers: answers}
	return OpEnv{StatsSystem: StatsSystem{GOOS: "linux"}, Exec: f.exec, Config: func() (Config, error) { return config, nil }}, f
}

func okResult(output string) ExecResult { return ExecResult{Output: output} }

func TestServiceStatusNeedsNoConfigurationAndCheckstheName(t *testing.T) {
	env, f := envWith(Config{}, map[string]ExecResult{
		"systemctl show -p LoadState --value -- nginx": okResult("loaded"),
		"systemctl is-active --quiet -- nginx":         okResult(""),
	})

	got := RunOperation(env, "systemctl.sh", []string{"nginx", "status"})
	want := Result{Status: "OK", Output: "OK", Service: "nginx", Message: "The service is running."}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	for _, name := range []string{"", "-rf", "a b", "a;b", "a$(b)", "../x", "a/b", strings.Repeat("a", 129), "--version"} {
		if r := RunOperation(env, "systemctl.sh", []string{name, "status"}); r.Status != "FAIL" || r.Message != "That is not a valid name." {
			t.Errorf("%q: %+v", name, r)
		}
	}
	for _, line := range f.lines() {
		if !strings.Contains(line, "nginx") {
			t.Errorf("a command was run for a name that is not valid: %s", line)
		}
	}
}

func TestSystemctlStatusStoppedAndUnknown(t *testing.T) {
	env, _ := envWith(Config{}, map[string]ExecResult{
		"systemctl show -p LoadState --value -- off":     okResult("loaded"),
		"systemctl is-active --quiet -- off":             {ExitCode: 3},
		"systemctl show -p LoadState --value -- missing": okResult("not-found"),
	})
	if r := RunOperation(env, "systemctl.sh", []string{"off", "status"}); r.Output != "NO" || r.Status != "OK" {
		t.Errorf("stopped: %+v", r)
	}
	if r := RunOperation(env, "systemctl.sh", []string{"missing", "status"}); r.Status != "FAIL" || r.Output != "UNKNOWN" {
		t.Errorf("missing: %+v", r)
	}
}

func TestChangingAServiceIsOnlyWhatTheConfigurationAllows(t *testing.T) {
	config := Config{Services: map[string][]string{"systemctl": {"nginx"}}}
	env, f := envWith(config, map[string]ExecResult{"systemctl restart nginx": okResult("")})

	if r := RunOperation(env, "systemctl.sh", []string{"nginx", "restart"}); r.Status != "OK" || r.Output != "OK" {
		t.Errorf("allowed: %+v", r)
	}

	r := RunOperation(env, "systemctl.sh", []string{"sshd", "restart"})
	if r.Status != "FAIL" || !strings.Contains(r.Message, "does not allow restart on sshd") {
		t.Errorf("not in the configuration: %+v", r)
	}
	if r := RunOperation(env, "systemctl.sh", []string{"nginx", "mask"}); r.Message != "Disallowed action." {
		t.Errorf("an action that is not one: %+v", r)
	}
	// It is the service of that manager that is allowed, not the name for every manager.
	if r := RunOperation(env, "docker.sh", []string{"nginx", "restart"}); r.Status != "FAIL" {
		t.Errorf("another manager: %+v", r)
	}
	if got := f.lines(); !reflect.DeepEqual(got, []string{"systemctl restart nginx"}) {
		t.Errorf("commands run: %v", got)
	}
}

func TestWithoutAConfigurationNothingChanges(t *testing.T) {
	env, f := envWith(Config{}, nil)
	for _, name := range []string{"systemctl.sh", "service.sh", "init_d.sh", "docker.sh", "pm2.sh", "supervisord.sh"} {
		if r := RunOperation(env, name, []string{"x", "restart"}); r.Status != "FAIL" {
			t.Errorf("%s changed something without the configuration: %+v", name, r)
		}
	}
	if len(f.lines()) != 0 {
		t.Errorf("commands were run: %v", f.lines())
	}
}

func TestAWrongConfigurationIsAFailureAndNothingRuns(t *testing.T) {
	f := &fakeExec{}
	env := OpEnv{Exec: f.exec, Config: func() (Config, error) { return Config{}, errors.New("line 3: not valid") }}

	for _, args := range [][]string{{"nginx", "restart"}} {
		r := RunOperation(env, "systemctl.sh", args)
		if r.Status != "FAIL" || !strings.Contains(r.Message, "line 3: not valid") {
			t.Errorf("%+v", r)
		}
	}
	if r := RunOperation(env, "action.sh", []string{"deploy"}); r.Status != "FAIL" || len(f.lines()) != 0 {
		t.Errorf("action with a wrong configuration: %+v %v", r, f.lines())
	}
}

func TestThePrivilegeIsPutBeforeWhatChangesAndNeverBeforeTheStatus(t *testing.T) {
	config := Config{Privilege: []string{"sudo", "-n"}, Services: map[string][]string{"docker": {"web"}}}
	env, f := envWith(config, map[string]ExecResult{
		"sudo -n docker restart web":                    okResult("web"),
		"docker inspect --format={{.State.Status}} web": okResult("running"),
	})

	RunOperation(env, "docker.sh", []string{"web", "restart"})
	if r := RunOperation(env, "docker.sh", []string{"web", "status"}); r.Output != "OK" {
		t.Errorf("status %+v", r)
	}
	if got := f.lines(); !reflect.DeepEqual(got, []string{"sudo -n docker restart web", "docker inspect --format={{.State.Status}} web"}) {
		t.Errorf("commands: %v", got)
	}
}

func TestEveryManagerBuildsItsCommand(t *testing.T) {
	cases := []struct {
		script, key, action string
		line                string
	}{
		{"service.sh", "service", "reload", "service cron reload"},
		{"init_d.sh", "init_d", "start", "/etc/init.d/cron start"},
		{"pm2.sh", "pm2", "reload", "pm2 reload cron"},
		{"supervisord.sh", "supervisord", "stop", "supervisorctl stop cron"},
		{"docker.sh", "docker", "start", "docker start cron"},
	}
	for _, c := range cases {
		config := Config{Services: map[string][]string{c.key: {"cron"}}}
		env, f := envWith(config, map[string]ExecResult{c.line: okResult("")})
		if r := RunOperation(env, c.script, []string{"cron", c.action}); r.Status != "OK" {
			t.Errorf("%s %s: %+v", c.script, c.action, r)
		}
		if got := f.lines(); !reflect.DeepEqual(got, []string{c.line}) {
			t.Errorf("%s: ran %v, want %s", c.script, got, c.line)
		}
	}

	// reload is not for every manager, as with the scripts of Dymmer.
	env, _ := envWith(Config{Services: map[string][]string{"docker": {"cron"}}}, nil)
	if r := RunOperation(env, "docker.sh", []string{"cron", "reload"}); r.Message != "Disallowed action." {
		t.Errorf("docker reload: %+v", r)
	}
}

func TestStatusOfTheOtherManagers(t *testing.T) {
	env, _ := envWith(Config{}, map[string]ExecResult{
		"service up status":                                okResult(""),
		"service down status":                              {ExitCode: 1},
		"/etc/init.d/up status":                            okResult(""),
		"pm2 describe on":                                  okResult("status │ online"),
		"pm2 describe off":                                 okResult("status │ stopped"),
		"supervisorctl status on":                          okResult("on RUNNING pid 5"),
		"supervisorctl status off":                         okResult("off EXITED"),
		"docker inspect --format={{.State.Status}} paused": okResult("paused"),
	})
	for _, c := range []struct {
		script, name, output string
	}{
		{"service.sh", "up", "OK"}, {"service.sh", "down", "NO"},
		{"init_d.sh", "up", "OK"},
		{"pm2.sh", "on", "OK"}, {"pm2.sh", "off", "NO"}, {"pm2.sh", "gone", "UNKNOWN"},
		{"supervisord.sh", "on", "OK"}, {"supervisord.sh", "off", "NO"}, {"supervisord.sh", "gone", "UNKNOWN"},
		{"docker.sh", "paused", "NO"}, {"docker.sh", "gone", "UNKNOWN"},
	} {
		if r := RunOperation(env, c.script, []string{c.name, "status"}); r.Output != c.output {
			t.Errorf("%s %s: %+v, want output %s", c.script, c.name, r, c.output)
		}
	}
}

func TestDockerComposeNeedsTheProjectAndTheServiceInTheConfiguration(t *testing.T) {
	config := Config{Privilege: []string{"sudo", "-n"}, Compose: []ComposeProject{{Path: "/srv/app", Services: []string{"web"}}}}
	env, f := envWith(config, map[string]ExecResult{
		"docker compose ps web":              okResult("web  Up 2 hours"),
		"sudo -n docker compose up -d web":   okResult(""),
		"sudo -n docker compose restart web": okResult(""),
	})

	if r := RunOperation(env, "docker_compose.sh", []string{"/srv/app", "web", "status"}); r.Output != "OK" {
		t.Errorf("status %+v", r)
	}
	if r := RunOperation(env, "docker_compose.sh", []string{"/srv/app", "web", "start"}); r.Status != "OK" {
		t.Errorf("start %+v", r)
	}
	if r := RunOperation(env, "docker_compose.sh", []string{"/srv/app", "web", "restart"}); r.Status != "OK" {
		t.Errorf("restart %+v", r)
	}
	for _, args := range [][]string{
		{"/srv/other", "web", "status"}, {"/srv/app/../other", "web", "status"}, {"/srv/app", "db", "status"}, {"/srv/app", "-f", "status"},
	} {
		if r := RunOperation(env, "docker_compose.sh", args); r.Status != "FAIL" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := RunOperation(env, "docker_compose.sh", []string{"/srv/app", "web", "down"}); r.Message != "Disallowed action." {
		t.Errorf("down %+v", r)
	}
	for _, spec := range f.ran {
		if spec.Dir != "/srv/app" {
			t.Errorf("a compose command ran in %q", spec.Dir)
		}
	}
}

func TestActionsRunWhatTheOwnerWroteUnderTheName(t *testing.T) {
	config := Config{
		Privilege: []string{"sudo", "-n"},
		Actions: map[string]ActionConfig{
			"deploy": {Command: []string{"/usr/local/bin/deploy", "--prod"}, Dir: "/srv/site", Env: map[string]string{"MODE": "prod"}, Timeout: "2m", Privileged: true},
			"plain":  {Command: []string{"/bin/true"}},
			"broken": {Command: []string{"/bin/false"}},
		},
	}
	env, f := envWith(config, map[string]ExecResult{
		"sudo -n /usr/local/bin/deploy --prod": okResult("deployed 3 files"),
		"/bin/true":                            okResult(""),
		"/bin/false":                           {ExitCode: 1, Output: "boom"},
	})

	if r := RunOperation(env, "action.sh", []string{"deploy"}); r.Status != "OK" || r.Message != "deployed 3 files" {
		t.Errorf("deploy %+v", r)
	}
	spec := f.ran[0]
	if spec.Dir != "/srv/site" || spec.Env["MODE"] != "prod" || spec.Timeout != 2*time.Minute {
		t.Errorf("the spec was %+v", spec)
	}
	if r := RunOperation(env, "action.sh", []string{"plain"}); r.Status != "OK" {
		t.Errorf("plain %+v", r)
	}
	if got := f.lines()[1]; got != "/bin/true" {
		t.Errorf("an action that is not privileged ran as %q", got)
	}
	if r := RunOperation(env, "action.sh", []string{"broken"}); r.Status != "FAIL" || !strings.Contains(r.Message, "code 1: boom") {
		t.Errorf("broken %+v", r)
	}
}

func TestAnActionThatIsNotWrittenOrNotANameIsRefused(t *testing.T) {
	env, f := envWith(Config{Actions: map[string]ActionConfig{"ok": {Command: []string{"/bin/true"}}}}, nil)
	for _, args := range [][]string{{"nope"}, {""}, {"ok; reboot"}, {"-x"}, {"ok", "extra"}, {}} {
		if r := RunOperation(env, "action.sh", args); r.Status != "FAIL" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if len(f.lines()) != 0 {
		t.Errorf("something ran: %v", f.lines())
	}
}

func TestAnActionThatTakesTooLongIsSaidSo(t *testing.T) {
	config := Config{Actions: map[string]ActionConfig{"slow": {Command: []string{"/bin/sleep", "9"}, Timeout: "1s"}}}
	env := OpEnv{Exec: func(context.Context, ExecSpec) ExecResult {
		return ExecResult{ExitCode: -1, Err: context.DeadlineExceeded}
	},
		Config: func() (Config, error) { return config, nil }}
	if r := RunOperation(env, "action.sh", []string{"slow"}); r.Status != "FAIL" || !strings.Contains(r.Message, "took longer than 1s") {
		t.Errorf("%+v", r)
	}
}

func TestLogsAreOnlyWhatIsDeclared(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	var lines []string
	for i := 1; i <= 800; i++ {
		lines = append(lines, "line "+itoa(i)+map[bool]string{true: " error", false: ""}[i%100 == 0])
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	_ = os.WriteFile(secret, []byte("password=hunter2\n"), 0o600)

	config := Config{Logs: LogsConfig{Files: []string{logPath}, Units: []string{"nginx"}}}
	env, f := envWith(config, map[string]ExecResult{
		"journalctl -n 500 --no-pager --output=cat --unit=nginx": okResult("a\nb error\nc\nd error"),
	})

	last := RunOperation(env, "log_recent_matching.sh", []string{"file", logPath})
	if last.Status != "OK" || !strings.HasSuffix(last.Message, "line 800 error") || strings.Count(last.Message, "\n") != 14 {
		t.Errorf("last lines: %+v", last)
	}
	// Among the last 500 lines (301 to 800), the ones with "error" are 400, 500, 600, 700 and 800.
	matching := RunOperation(env, "log_recent_matching.sh", []string{"file", logPath, "error$"})
	if matching.Message != "line 400 error\nline 500 error\nline 600 error\nline 700 error\nline 800 error" {
		t.Errorf("matching: %q", matching.Message)
	}
	if r := RunOperation(env, "log_recent_matching.sh", []string{"journal", "nginx", "error"}); r.Message != "b error\nd error" {
		t.Errorf("journal: %+v", r)
	}

	for _, args := range [][]string{
		{"file", secret}, {"file", "/etc/passwd"}, {"file", logPath + "/../secret.txt"}, {"file", dir + "/../" + filepath.Base(dir) + "/secret.txt"},
		{"journal", "sshd"}, {"journal", "--since=2000-01-01"}, {"journal", "nginx --all"}, {"socket", logPath}, {"file"}, {"file", logPath, "(", "x"},
	} {
		if r := RunOperation(env, "log_recent_matching.sh", args); r.Status != "FAIL" {
			t.Errorf("%v was answered: %+v", args, r)
		}
	}
	if r := RunOperation(env, "log_recent_matching.sh", []string{"file", logPath, "("}); r.Message != "The filter is not a valid expression." {
		t.Errorf("bad filter: %+v", r)
	}
	if r := RunOperation(env, "log_recent_matching.sh", []string{"file", logPath, strings.Repeat("a", 300)}); r.Message != "The filter is too long." {
		t.Errorf("long filter: %+v", r)
	}
	for _, line := range f.lines() {
		if !strings.HasSuffix(line, "--unit=nginx") {
			t.Errorf("a command ran for something not declared: %s", line)
		}
	}
}

func TestAFileThatIsNotAFileIsNotRead(t *testing.T) {
	dir := t.TempDir()
	config := Config{Logs: LogsConfig{Files: []string{dir}}}
	env, _ := envWith(config, nil)
	if r := RunOperation(env, "log_recent_matching.sh", []string{"file", dir}); r.Status != "FAIL" {
		t.Errorf("a directory was read: %+v", r)
	}
}
