package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// State is what the daemon keeps after pairing: which host it is and the permanent secret that proves it. The
// secret lets anyone connect as this host, so the file is readable only by its owner.
type State struct {
	HostID string `json:"host_id"`
	Secret string `json:"secret"`
}

// ErrNotPaired is returned when there is no state yet: `dym auth daemon` has not been run on this host.
var ErrNotPaired = errors.New("this host is not paired yet: run `dym auth daemon`")

// DefaultStatePath is where the state lives: /var/lib/dym for root (the service), the user's config otherwise.
func DefaultStatePath() (string, error) {
	if os.Geteuid() == 0 {
		return "/var/lib/dym/daemon.json", nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot find a place for the state: %w", err)
	}
	return filepath.Join(dir, "dym", "daemon.json"), nil
}

// LoadState reads the state, or ErrNotPaired when there is none.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, ErrNotPaired
	}
	if err != nil {
		return State{}, err
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("the state file %s is not valid: %w", path, err)
	}
	if s.HostID == "" || s.Secret == "" {
		return State{}, ErrNotPaired
	}
	return s, nil
}

// SaveState writes the state so that nobody else can read it, and so that a crash never leaves half of it: it is
// written aside and renamed.
func SaveState(path string, s State) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".daemon-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
