package credentials

import (
	"errors"
	"testing"
)

type fakeStore struct {
	token string
	err   error
}

func (s *fakeStore) Get() (string, error)   { return s.token, s.err }
func (s *fakeStore) Set(token string) error { s.token = token; return s.err }
func (s *fakeStore) Delete() error          { s.token = ""; return s.err }

func TestResolvePrefersEnvironment(t *testing.T) {
	token, source, err := Resolve(func(string) string { return "env-token" }, &fakeStore{token: "keychain-token"})
	if err != nil || token != "env-token" || source != "environment" {
		t.Fatalf("Resolve() = %q, %q, %v", token, source, err)
	}
}

func TestResolveReturnsKeychainError(t *testing.T) {
	want := errors.New("keychain unavailable")
	_, _, err := Resolve(func(string) string { return "" }, &fakeStore{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("Resolve() error = %v", err)
	}
}
