package credentials

import (
	"strings"

	"github.com/zalando/go-keyring"
)

const service = "dym"
const account = "api-token"

type Store interface {
	Get() (string, error)
	Set(string) error
	Delete() error
}

type KeyringStore struct{}

func (KeyringStore) Get() (string, error)   { return keyring.Get(service, account) }
func (KeyringStore) Set(token string) error { return keyring.Set(service, account, token) }
func (KeyringStore) Delete() error          { return keyring.Delete(service, account) }

func Resolve(env func(string) string, store Store) (token, source string, err error) {
	if token = strings.TrimSpace(env("DYMMER_TOKEN")); token != "" {
		return token, "environment", nil
	}
	token, err = store.Get()
	if err != nil {
		return "", "", err
	}
	return token, "keychain", nil
}
