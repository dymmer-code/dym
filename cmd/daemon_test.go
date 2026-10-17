package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dymmer-code/dym/internal/daemon"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestAuthDaemonShowsThePINAndSaysWhichHostItBecame(t *testing.T) {
	out := new(bytes.Buffer)
	var got daemon.PairOptions
	cmd := NewRootCommand(Dependencies{
		Out: out, Err: new(bytes.Buffer), Env: env(nil),
		Inventory: func() daemon.Inventory { return daemon.Inventory{Hostname: "rowan"} },
		Pair: func(_ context.Context, o daemon.PairOptions) (daemon.State, error) {
			got = o
			o.OnPIN("ABCD-EFGH", 120*time.Second)
			return daemon.State{HostID: "host-1", Secret: "dymd_THE_SECRET"}, nil
		},
	})
	cmd.SetArgs([]string{"auth", "daemon", "--state", "/tmp/x/daemon.json"})

	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	text := out.String()
	for _, want := range []string{"Pairing code: ABCD-EFGH", "within 2m0s", "host-1", "dym daemon"} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "dymd_THE_SECRET") {
		t.Error("the secret must never be printed")
	}
	if got.URL != "wss://dymmer.com/daemon/websocket" || got.StatePath != "/tmp/x/daemon.json" || got.Inventory.Hostname != "rowan" {
		t.Errorf("options = %+v", got)
	}
}

func TestTheServerComesFromTheFlagThenFromTheEnvironmentThenFromTheDefault(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"default", nil, nil, "wss://dymmer.com/daemon/websocket"},
		{"environment", map[string]string{"DYM_SERVER": "http://localhost:4000"}, nil, "ws://localhost:4000/daemon/websocket"},
		{"flag wins", map[string]string{"DYM_SERVER": "http://localhost:4000"}, []string{"--server", "https://dymmer.example"}, "wss://dymmer.example/daemon/websocket"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got daemon.RunOptions
			cmd := NewRootCommand(Dependencies{
				Out: new(bytes.Buffer), Err: new(bytes.Buffer), Env: env(c.env),
				RunDaemon: func(_ context.Context, o daemon.RunOptions) error { got = o; return nil },
			})
			cmd.SetArgs(append([]string{"daemon", "--state", "/tmp/d.json"}, c.args...))

			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if got.URL != c.want || got.StatePath != "/tmp/d.json" || got.Logger == nil {
				t.Errorf("options = %+v", got)
			}
		})
	}
}

func TestAnAddressThatIsNotAServerFailsBeforeAnythingConnects(t *testing.T) {
	called := false
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), Env: env(nil),
		Pair: func(context.Context, daemon.PairOptions) (daemon.State, error) {
			called = true
			return daemon.State{}, nil
		},
		RunDaemon: func(context.Context, daemon.RunOptions) error { called = true; return nil },
	})

	for _, args := range [][]string{{"auth", "daemon", "--server", "dymmer.com"}, {"daemon", "--server", "ftp://x"}} {
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("%v must fail", args)
		}
	}
	if called {
		t.Error("nothing must connect with a wrong address")
	}
}

func TestAuthDaemonAndDaemonReportWhatWentWrong(t *testing.T) {
	cmd := NewRootCommand(Dependencies{
		Out: new(bytes.Buffer), Err: new(bytes.Buffer), Env: env(nil),
		Pair: func(context.Context, daemon.PairOptions) (daemon.State, error) {
			return daemon.State{}, daemon.ErrPairingExpired
		},
		RunDaemon: func(context.Context, daemon.RunOptions) error { return errors.New("boom") },
	})

	cmd.SetArgs([]string{"auth", "daemon", "--state", "/tmp/d.json"})
	if err := cmd.Execute(); !errors.Is(err, daemon.ErrPairingExpired) {
		t.Errorf("auth daemon: %v", err)
	}

	cmd.SetArgs([]string{"daemon", "--state", "/tmp/d.json"})
	if err := cmd.Execute(); err == nil || err.Error() != "boom" {
		t.Errorf("daemon: %v", err)
	}
}

func TestTheDaemonCommandsTakeNoArguments(t *testing.T) {
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), Env: env(nil)})
	for _, args := range [][]string{{"daemon", "extra"}, {"auth", "daemon", "extra"}} {
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("%v must be refused", args)
		}
	}
}

func TestDaemonChecksTheConfigurationWithoutConnecting(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	bad := filepath.Join(dir, "bad.yaml")
	_ = os.WriteFile(good, []byte("services:\n  systemctl: [nginx]\n"), 0o600)
	_ = os.WriteFile(bad, []byte("services:\n  systemctl: ['a b']\n"), 0o600)

	ran := false
	check := func(path string) (string, error) {
		out := new(bytes.Buffer)
		cmd := NewRootCommand(Dependencies{
			Out: out, Err: new(bytes.Buffer), Env: env(nil),
			RunDaemon: func(context.Context, daemon.RunOptions) error { ran = true; return nil },
		})
		cmd.SetArgs([]string{"daemon", "--check-config", "--config", path})
		err := cmd.Execute()
		return out.String(), err
	}

	if out, err := check(good); err != nil || !strings.Contains(out, "is valid") {
		t.Errorf("good: %q %v", out, err)
	}
	if _, err := check(bad); err == nil || !strings.Contains(err.Error(), "not a valid name") {
		t.Errorf("bad: %v", err)
	}
	if ran {
		t.Error("checking the configuration connected the daemon")
	}
}
