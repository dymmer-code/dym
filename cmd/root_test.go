package cmd

import (
	"bytes"
	"testing"
)

// panicOnTouchStore is a credentials.Store spy that fails the test if any of
// its methods are ever called. It locks in the architectural invariant that
// NewRootCommand must never touch the credential store: credential/API-client
// resolution must stay lazy, happening only inside a command's own RunE
// (see resolveAPI in domain.go), never eagerly during command construction,
// so that credential-free commands like "dym auth login" and
// "dym auth token-help" keep working with zero stored credentials.
type panicOnTouchStore struct {
	t *testing.T
}

func (s panicOnTouchStore) Get() (string, error) {
	s.t.Fatal("credential store Get() must not be called for a credential-free command path")
	return "", nil
}

func (s panicOnTouchStore) Set(string) error {
	s.t.Fatal("credential store Set() must not be called for a credential-free command path")
	return nil
}

func (s panicOnTouchStore) Delete() error {
	s.t.Fatal("credential store Delete() must not be called for a credential-free command path")
	return nil
}

func TestNewRootCommandNeverTouchesCredentialStoreOnCredentialFreePath(t *testing.T) {
	store := panicOnTouchStore{t: t}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	deps := Dependencies{Out: out, Err: errOut, Store: store}

	// Constructing the root command must not touch the store.
	cmd := NewRootCommand(deps)

	// Executing a credential-free command (auth token-help) must not touch
	// the store either.
	cmd.SetArgs([]string{"auth", "token-help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestNewRootCommandHelpDoesNotTouchCredentialStore(t *testing.T) {
	store := panicOnTouchStore{t: t}
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	deps := Dependencies{Out: out, Err: errOut, Store: store}

	cmd := NewRootCommand(deps)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}
