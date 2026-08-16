package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/dymmer-code/dym/internal/confirm"
	"github.com/dymmer-code/dym/internal/credentials"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type Dependencies struct {
	Out, Err   io.Writer
	Store      credentials.Store
	Env        func(string) string
	ReadSecret func(prompt string) (string, error)

	// API, when set, is used instead of resolving a real *api.Client from
	// stored credentials. Left nil in production so credential resolution
	// stays lazy: it happens inside each domain/mailbox/forwarding
	// command's RunE (see resolveAPI in domain.go), never here, so that
	// credential-free commands like "auth login" keep working with zero
	// stored credentials.
	API APIClient
	// BaseURL is the Dymmer API base URL; defaults to
	// "https://dymmer.com/api/v1" when empty.
	BaseURL string
	// IsTerminal reports whether stdin is an interactive terminal; used to
	// decide whether a destructive command may prompt for confirmation.
	IsTerminal func() bool
	// Confirm prompts the user with a yes/no question and reports the
	// answer. It defaults to a real stderr/stdin prompt (internal/confirm).
	Confirm func(prompt string) (bool, error)
}

func NewRootCommand(deps Dependencies) *cobra.Command {
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	if deps.Err == nil {
		deps.Err = os.Stderr
	}
	if deps.Store == nil {
		deps.Store = credentials.KeyringStore{}
	}
	if deps.Env == nil {
		deps.Env = os.Getenv
	}
	if deps.ReadSecret == nil {
		deps.ReadSecret = readSecretFromTerminal
	}
	if deps.BaseURL == "" {
		deps.BaseURL = defaultBaseURL
	}
	if deps.IsTerminal == nil {
		deps.IsTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	}
	if deps.Confirm == nil {
		deps.Confirm = func(prompt string) (bool, error) {
			return confirm.Prompt(os.Stderr, os.Stdin, prompt)
		}
	}
	cmd := &cobra.Command{Use: "dym", Short: "Dymmer command-line client", SilenceUsage: true}
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	cmd.AddCommand(newAuthCommand(deps))
	cmd.AddCommand(newDomainCommand(deps))
	return cmd
}

// readSecretFromTerminal prompts on stderr and reads a token without echoing
// it when stdin is a terminal. It falls back to reading a line from stdin
// so tokens can still be piped in non-interactive contexts.
func readSecretFromTerminal(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		secret, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(secret), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return line, nil
}
