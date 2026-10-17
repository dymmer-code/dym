package daemon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// maxOutput bounds what is kept of the output of a command.
const maxOutput = 64 * 1024

// errEmptyDocument is what the YAML decoder says for a file without content.
var errEmptyDocument = io.EOF

func bytesReader(data []byte) io.Reader { return bytes.NewReader(data) }

// ExecSpec is a command to run. It is a list of arguments and never a line for a shell: nothing in it is interpreted.
type ExecSpec struct {
	Argv    []string
	Dir     string
	Env     map[string]string
	Timeout time.Duration
}

// ExecResult is how it ended. ExitCode is -1 when it did not start or did not end by itself.
type ExecResult struct {
	Output   string // standard output and error together, the end of it if it was long
	ExitCode int
	Err      error
}

// ExecFunc runs a command; tests give their own.
type ExecFunc func(ctx context.Context, spec ExecSpec) ExecResult

// pathOfCommands is where commands are found: the service has no login, so it does not have the PATH of one.
const pathOfCommands = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// HostExec runs the command on this host without a shell, in its own process group so that a timeout ends the
// children too, with a clean environment and the output bounded.
func HostExec(ctx context.Context, spec ExecSpec) ExecResult {
	if len(spec.Argv) == 0 {
		return ExecResult{ExitCode: -1, Err: errors.New("no command")}
	}
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}

	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir = spec.Dir
	cmd.Env = []string{"PATH=" + pathOfCommands, "LC_ALL=C", "HOME=" + os.TempDir()}
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	ownProcessGroup(cmd)

	out := &tailBuffer{max: maxOutput}
	cmd.Stdout, cmd.Stderr = out, out

	if err := cmd.Start(); err != nil {
		return ExecResult{ExitCode: -1, Err: err}
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return ExecResult{Output: out.String(), ExitCode: exitCode(err), Err: nonExit(err)}
	case <-ctx.Done():
		killGroup(cmd.Process.Pid)
		<-done
		return ExecResult{Output: out.String(), ExitCode: -1, Err: ctx.Err()}
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}

// nonExit is the error when the command failed in another way than ending with a code.
func nonExit(err error) error {
	var exit *exec.ExitError
	if err == nil || errors.As(err, &exit) {
		return nil
	}
	return err
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return strings.TrimSpace(string(t.buf)) }
