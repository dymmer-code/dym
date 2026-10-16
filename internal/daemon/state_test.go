package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStateRoundTripAndNobodyElseCanReadIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "daemon.json")

	if err := SaveState(path, State{HostID: "h1", Secret: "dymd_s"}); err != nil {
		t.Fatal(err)
	}

	got, err := LoadState(path)
	if err != nil || got != (State{HostID: "h1", Secret: "dymd_s"}) {
		t.Fatalf("got %+v, %v", got, err)
	}

	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file mode %o, want 600", info.Mode().Perm())
	}
	dir, _ := os.Stat(filepath.Dir(path))
	if dir.Mode().Perm() != 0o700 {
		t.Errorf("directory mode %o, want 700", dir.Mode().Perm())
	}
}

func TestSaveStateLeavesNothingElseBehindAndReplacesTheOldOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.json")

	_ = SaveState(path, State{HostID: "old", Secret: "old"})
	_ = SaveState(path, State{HostID: "new", Secret: "new"})

	got, _ := LoadState(path)
	if got.HostID != "new" {
		t.Errorf("got %+v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the state file, found %d entries", len(entries))
	}
}

func TestLoadStateWithoutAFileOrWithAnEmptyOneIsNotPaired(t *testing.T) {
	dir := t.TempDir()

	if _, err := LoadState(filepath.Join(dir, "missing.json")); !errors.Is(err, ErrNotPaired) {
		t.Errorf("missing: %v", err)
	}

	empty := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(empty, []byte(`{"host_id":"","secret":""}`), 0o600)
	if _, err := LoadState(empty); !errors.Is(err, ErrNotPaired) {
		t.Errorf("empty: %v", err)
	}
}

func TestLoadStateOfACorruptFileSaysSoAndIsNotTakenAsNotPaired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o600)

	_, err := LoadState(path)
	if err == nil || errors.Is(err, ErrNotPaired) {
		t.Errorf("a corrupt file must be an error of its own, got %v", err)
	}
}
