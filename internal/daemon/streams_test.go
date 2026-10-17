package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// collector gathers what a stream emits.
type collector struct {
	mu   sync.Mutex
	text strings.Builder
}

func (c *collector) emit(data string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text.WriteString(data)
}

func (c *collector) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return splitLines(c.text.String())
}

func (c *collector) waitFor(t *testing.T, what string, condition func([]string) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition(c.lines()) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; got %q", what, c.lines())
}

func followEnv(files ...string) OpEnv {
	return OpEnv{Config: func() (Config, error) { return Config{Logs: LogsConfig{Files: files, Units: []string{"nginx"}}}, nil }}
}

func startFollow(t *testing.T, env OpEnv, args ...string) (*collector, func() Result) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c := &collector{}
	done := make(chan Result, 1)
	go func() { done <- followLog(ctx, env, args, c.emit) }()
	t.Cleanup(cancel)
	return c, func() Result {
		cancel()
		select {
		case r := <-done:
			return r
		case <-time.After(5 * time.Second):
			t.Fatal("the stream did not stop")
			return Result{}
		}
	}
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

func TestFollowingAFileShowsTheLastLinesAndThenWhatIsAdded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	var initial strings.Builder
	for i := 1; i <= 30; i++ {
		initial.WriteString("old " + itoa(i) + "\n")
	}
	_ = os.WriteFile(path, []byte(initial.String()), 0o600)

	c, stop := startFollow(t, followEnv(path), "file", path)
	c.waitFor(t, "the last ten lines", func(l []string) bool { return len(l) == 10 && l[0] == "old 21" && l[9] == "old 30" })

	appendTo(t, path, "new 1\nnew 2\n")
	c.waitFor(t, "the new lines", func(l []string) bool { return len(l) == 12 && l[10] == "new 1" && l[11] == "new 2" })

	// A line that is not finished waits for its end.
	appendTo(t, path, "half")
	time.Sleep(2 * followPoll)
	if got := c.lines(); len(got) != 12 {
		t.Errorf("an unfinished line was sent: %q", got)
	}
	appendTo(t, path, " done\n")
	c.waitFor(t, "the finished line", func(l []string) bool { return len(l) == 13 && l[12] == "half done" })

	if r := stop(); r.Status != "OK" {
		t.Errorf("a stop is not a failure: %+v", r)
	}
}

func TestFollowingAFileWithAFilterSendsOnlyTheLinesThatMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	_ = os.WriteFile(path, []byte("ERROR old\ninfo old\n"), 0o600)

	c, stop := startFollow(t, followEnv(path), "file", path, "ERROR|WARN")
	c.waitFor(t, "the old line that matches", func(l []string) bool { return len(l) == 1 && l[0] == "ERROR old" })

	appendTo(t, path, "info a\nWARN b\ninfo c\nERROR d\n")
	c.waitFor(t, "the lines that match", func(l []string) bool { return len(l) == 3 && l[1] == "WARN b" && l[2] == "ERROR d" })
	stop()
}

func TestFollowingAFileGoesOnAfterATruncationAndARotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	_ = os.WriteFile(path, []byte("first line here\n"), 0o600)

	c, stop := startFollow(t, followEnv(path), "file", path)
	c.waitFor(t, "the first line", func(l []string) bool { return len(l) == 1 })

	if err := os.WriteFile(path, []byte("cut\n"), 0o600); err != nil { // the same file, now shorter
		t.Fatal(err)
	}
	c.waitFor(t, "the line after the truncation", func(l []string) bool { return len(l) == 2 && l[1] == "cut" })

	if err := os.Rename(path, path+".1"); err != nil { // rotated: the path is a new file
		t.Fatal(err)
	}
	appendTo(t, path, "in the new file\n")
	c.waitFor(t, "the line of the new file", func(l []string) bool { return len(l) == 3 && l[2] == "in the new file" })
	stop()
}

func TestOnlyWhatIsDeclaredCanBeFollowed(t *testing.T) {
	dir := t.TempDir()
	declared := filepath.Join(dir, "ok.log")
	other := filepath.Join(dir, "other.log")
	_ = os.WriteFile(declared, []byte("x\n"), 0o600)
	_ = os.WriteFile(other, []byte("secret\n"), 0o600)
	env := followEnv(declared)

	for _, args := range [][]string{{"file", other}, {"journal", "sshd"}, {"socket", declared}, {"file"}, {"file", declared, "("}} {
		c := &collector{}
		r := followLog(context.Background(), env, args, c.emit)
		if r.Status != "FAIL" || len(c.lines()) != 0 {
			t.Errorf("%v: %+v sent %q", args, r, c.lines())
		}
	}
}

func TestFollowingTheJournalUsesTheDeclaredUnitAndFilters(t *testing.T) {
	var ran ExecSpec
	env := followEnv()
	env.Stream = func(ctx context.Context, spec ExecSpec, onLine func(string)) ExecResult {
		ran = spec
		for _, line := range []string{"started", "ERROR boom", "finished"} {
			onLine(line)
		}
		<-ctx.Done()
		return ExecResult{ExitCode: -1, Err: ctx.Err()}
	}

	c, stop := startFollow(t, env, "journal", "nginx", "ERROR")
	c.waitFor(t, "the line that matches", func(l []string) bool { return len(l) == 1 && l[0] == "ERROR boom" })
	stop()

	if strings.Join(ran.Argv, " ") != "journalctl -f --no-pager --output=cat --unit=nginx" {
		t.Errorf("ran %v", ran.Argv)
	}
}

func TestAnEndedJournalIsAFailureThatSaysSo(t *testing.T) {
	env := followEnv()
	env.Stream = func(context.Context, ExecSpec, func(string)) ExecResult {
		return ExecResult{ExitCode: 1, Output: "no such unit"}
	}
	r := followLog(context.Background(), env, []string{"journal", "nginx"}, func(string) {})
	if r.Status != "FAIL" {
		t.Errorf("%+v", r)
	}
}

func TestTheNameOfAStreamIsAnOperationOfTheDaemon(t *testing.T) {
	found := false
	for _, name := range OperationNames() {
		found = found || name == "log_follow.sh"
	}
	if !found {
		t.Errorf("the daemon does not say it answers log_follow.sh: %v", OperationNames())
	}
	// Asked as a one-off order it is not one: it only runs as a stream.
	if r := RunOperation(OpEnv{}, "log_follow.sh", []string{"file", "/x"}); r.Message != "unknown operation" {
		t.Errorf("%+v", r)
	}
}
