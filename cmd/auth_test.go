package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type fakeStore struct {
	token string
	err   error
}

func (s *fakeStore) Get() (string, error)   { return s.token, s.err }
func (s *fakeStore) Set(token string) error { s.token = token; return s.err }
func (s *fakeStore) Delete() error          { s.token = ""; return s.err }

func TestTokenHelpExplainsHowToGetToken(t *testing.T) {
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer)})
	cmd.SetArgs([]string{"auth", "token-help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://dymmer.com/keys") {
		t.Fatalf("help = %q", out.String())
	}
}

func TestLoginDoesNotWriteToken(t *testing.T) {
	out, errOut, store := new(bytes.Buffer), new(bytes.Buffer), &fakeStore{}
	cmd := NewRootCommand(Dependencies{Out: out, Err: errOut, Store: store, ReadSecret: func(string) (string, error) { return "secret-token", nil }})
	cmd.SetArgs([]string{"auth", "login"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if store.token != "secret-token" || strings.Contains(out.String()+errOut.String(), "secret-token") {
		t.Fatal("token leaked")
	}
}

func TestLoginRejectsEmptyToken(t *testing.T) {
	store := &fakeStore{}
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), Store: store, ReadSecret: func(string) (string, error) { return "   ", nil }})
	cmd.SetArgs([]string{"auth", "login"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for empty token")
	}
	if store.token != "" {
		t.Fatal("empty token must not be stored")
	}
}

func TestLoginReportsUnavailableKeychain(t *testing.T) {
	store := &fakeStore{err: errors.New("keychain unavailable")}
	cmd := NewRootCommand(Dependencies{Out: new(bytes.Buffer), Err: new(bytes.Buffer), Store: store, ReadSecret: func(string) (string, error) { return "secret-token", nil }})
	cmd.SetArgs([]string{"auth", "login"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when the keychain is unavailable")
	}
}

func TestLogoutRemovesTokenWithoutLeaking(t *testing.T) {
	out, store := new(bytes.Buffer), &fakeStore{token: "secret-token"}
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), Store: store})
	cmd.SetArgs([]string{"auth", "logout"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if store.token != "" {
		t.Fatal("token was not removed")
	}
	if strings.Contains(out.String(), "secret-token") {
		t.Fatal("token leaked")
	}
}

func TestStatusReportsEnvironmentSourceWithoutToken(t *testing.T) {
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), Store: &fakeStore{}, Env: func(string) string { return "env-token" }})
	cmd.SetArgs([]string{"auth", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "environment") || strings.Contains(out.String(), "env-token") {
		t.Fatalf("status = %q", out.String())
	}
}

func TestStatusReportsKeychainSourceWithoutToken(t *testing.T) {
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), Store: &fakeStore{token: "keychain-token"}, Env: func(string) string { return "" }})
	cmd.SetArgs([]string{"auth", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "keychain") || strings.Contains(out.String(), "keychain-token") {
		t.Fatalf("status = %q", out.String())
	}
}

func TestStatusReportsUnavailableCredentials(t *testing.T) {
	out := new(bytes.Buffer)
	cmd := NewRootCommand(Dependencies{Out: out, Err: new(bytes.Buffer), Store: &fakeStore{err: errors.New("no token")}, Env: func(string) string { return "" }})
	cmd.SetArgs([]string{"auth", "status"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No credentials") {
		t.Fatalf("status = %q", out.String())
	}
}
