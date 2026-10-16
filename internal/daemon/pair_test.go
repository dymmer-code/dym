package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func pairServer(t *testing.T, then func(c *fakeConn)) *fakeServer {
	t.Helper()
	f := newFakeServer(t)
	f.Join = func(c *fakeConn, topic string, _ map[string]any) (string, map[string]any) {
		if topic == "daemon:pair" && then != nil {
			go func() {
				time.Sleep(30 * time.Millisecond) // after the reply of the join
				then(c)
			}()
		}
		return "ok", map[string]any{"pin": "ABCD-EFGH", "expires_in": float64(120)}
	}
	return f
}

func TestPairShowsThePINAndKeepsTheSecretThatArrives(t *testing.T) {
	f := pairServer(t, func(c *fakeConn) {
		c.Push("daemon:pair", "paired", map[string]any{"host_id": "host-1", "secret": "dymd_abc"})
	})
	path := filepath.Join(t.TempDir(), "state", "daemon.json")

	var shown string
	var validFor time.Duration
	state, err := Pair(ctxTimeout(t), PairOptions{
		URL: f.URL(), StatePath: path, Inventory: Inventory{Hostname: "rowan", CPUCores: 4},
		OnPIN: func(pin string, d time.Duration) { shown, validFor = pin, d },
	})
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	if shown != "ABCD-EFGH" || validFor != 120*time.Second {
		t.Errorf("OnPIN got %q for %v", shown, validFor)
	}
	if state != (State{HostID: "host-1", Secret: "dymd_abc"}) {
		t.Errorf("state = %+v", state)
	}
	saved, err := LoadState(path)
	if err != nil || saved != state {
		t.Errorf("saved = %+v, %v", saved, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", info.Mode().Perm())
	}
}

func TestPairTellsDymmerWhatItKnowsAboutTheHost(t *testing.T) {
	f := pairServer(t, func(c *fakeConn) {
		c.Push("daemon:pair", "paired", map[string]any{"host_id": "h", "secret": "s"})
	})

	_, err := Pair(ctxTimeout(t), PairOptions{
		URL: f.URL(), StatePath: filepath.Join(t.TempDir(), "d.json"),
		Inventory: Inventory{Hostname: "rowan", CPUCores: 4},
	})
	if err != nil {
		t.Fatal(err)
	}

	joins := f.Joins()
	if len(joins) != 1 || joins[0].Topic != "daemon:pair" {
		t.Fatalf("joins = %+v", joins)
	}
	inv, _ := joins[0].Payload["inventory"].(map[string]any)
	if inv["hostname"] != "rowan" || inv["cpu_cores"] != float64(4) {
		t.Errorf("inventory = %v", inv)
	}
	// pairing is the one connection without credentials
	if h := f.Headers()[0]; h.Get("X-Dym-Host") != "" || h.Get("X-Dym-Secret") != "" {
		t.Error("the pairing connection must carry no credentials")
	}
}

func TestPairExpiredWritesNothing(t *testing.T) {
	f := pairServer(t, func(c *fakeConn) { c.Push("daemon:pair", "expired", map[string]any{}) })
	path := filepath.Join(t.TempDir(), "daemon.json")

	_, err := Pair(ctxTimeout(t), PairOptions{URL: f.URL(), StatePath: path})

	if !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("an expired PIN must leave no state")
	}
}

func TestPairWhenDymmerRefusesTheJoin(t *testing.T) {
	f := newFakeServer(t)
	f.Join = func(*fakeConn, string, map[string]any) (string, map[string]any) {
		return "error", map[string]any{"reason": "no_free_pin"}
	}

	_, err := Pair(ctxTimeout(t), PairOptions{URL: f.URL(), StatePath: filepath.Join(t.TempDir(), "d.json")})

	var refused *JoinError
	if !errors.As(err, &refused) || refused.Reason != "no_free_pin" {
		t.Fatalf("err = %v", err)
	}
}

func TestPairWithAnIncompletePairingSavesNothing(t *testing.T) {
	f := pairServer(t, func(c *fakeConn) {
		c.Push("daemon:pair", "paired", map[string]any{"host_id": "h"}) // no secret
	})
	path := filepath.Join(t.TempDir(), "daemon.json")

	if _, err := Pair(ctxTimeout(t), PairOptions{URL: f.URL(), StatePath: path}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Error("nothing must be saved")
	}
}

func TestPairWhenTheSecretCannotBeSavedSaysWhichHostToDelete(t *testing.T) {
	f := pairServer(t, func(c *fakeConn) {
		c.Push("daemon:pair", "paired", map[string]any{"host_id": "host-9", "secret": "dymd_s"})
	})
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, []byte("x"), 0o600)

	_, err := Pair(ctxTimeout(t), PairOptions{URL: f.URL(), StatePath: filepath.Join(blocker, "sub", "daemon.json")})

	if err == nil || !contains(err.Error(), "host-9") {
		t.Fatalf("the error must name the host that was created, got %v", err)
	}
}

func TestPairStopsWhenTheContextEnds(t *testing.T) {
	f := pairServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := Pair(ctx, PairOptions{URL: f.URL(), StatePath: filepath.Join(t.TempDir(), "d.json")})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestPairWhenTheConnectionEndsBeforeThePINIsEntered(t *testing.T) {
	f := pairServer(t, func(c *fakeConn) { c.Drop() })

	_, err := Pair(ctxTimeout(t), PairOptions{URL: f.URL(), StatePath: filepath.Join(t.TempDir(), "d.json")})

	if err == nil {
		t.Fatal("expected an error")
	}
}

func contains(text, part string) bool {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
