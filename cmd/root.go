package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/dymmer-code/dym/internal/credentials"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type Dependencies struct {
	Out, Err   io.Writer
	Store      credentials.Store
	Env        func(string) string
	ReadSecret func(prompt string) (string, error)
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
	cmd := &cobra.Command{Use: "dym", Short: "Dymmer command-line client", SilenceUsage: true}
	cmd.SetOut(deps.Out)
	cmd.SetErr(deps.Err)
	cmd.AddCommand(newAuthCommand(deps))
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
