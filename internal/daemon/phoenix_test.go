package daemon

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func ctxTimeout(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dial(t *testing.T, f *fakeServer, header http.Header) *Conn {
	t.Helper()
	c, err := Dial(ctxTimeout(t), f.URL(), header)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestDialSendsTheVersionInTheQueryAndTheCredentialsInHeaders(t *testing.T) {
	f := newFakeServer(t)
	h := http.Header{}
	h.Set("X-Dym-Host", "host-1")
	h.Set("X-Dym-Secret", "dymd_secret")

	dial(t, f, h)

	eventually(t, "the handshake", func() bool { return len(f.Headers()) == 1 })
	if got := f.Headers()[0].Get("X-Dym-Host"); got != "host-1" {
		t.Errorf("X-Dym-Host = %q", got)
	}
	if got := f.Headers()[0].Get("X-Dym-Secret"); got != "dymd_secret" {
		t.Errorf("X-Dym-Secret = %q", got)
	}
	if q := f.Queries()[0]; q != "vsn=2.0.0" {
		t.Errorf("the credentials must not be in the URL, query = %q", q)
	}
}

func TestJoinReturnsTheAnswerOfTheServer(t *testing.T) {
	f := newFakeServer(t)
	f.Join = func(_ *fakeConn, topic string, payload map[string]any) (string, map[string]any) {
		return "ok", map[string]any{"pin": "ABCD-EFGH", "topic": topic, "got": payload["x"]}
	}

	c := dial(t, f, nil)
	resp, err := c.Join(ctxTimeout(t), "daemon:pair", map[string]any{"x": "y"})
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if resp["pin"] != "ABCD-EFGH" || resp["topic"] != "daemon:pair" || resp["got"] != "y" {
		t.Errorf("response = %v", resp)
	}
}

func TestJoinRefusedIsAJoinErrorWithItsReason(t *testing.T) {
	f := newFakeServer(t)
	f.Join = func(*fakeConn, string, map[string]any) (string, map[string]any) {
		return "error", map[string]any{"reason": "not_allowed"}
	}

	c := dial(t, f, nil)
	_, err := c.Join(ctxTimeout(t), "daemon:host", nil)

	var refused *JoinError
	if !errors.As(err, &refused) || refused.Reason != "not_allowed" || refused.Topic != "daemon:host" {
		t.Fatalf("err = %v", err)
	}
}

func TestPushesOfTheServerArriveAsEvents(t *testing.T) {
	f := newFakeServer(t)
	c := dial(t, f, nil)
	if _, err := c.Join(ctxTimeout(t), "daemon:pair", nil); err != nil {
		t.Fatal(err)
	}

	f.Last().Push("daemon:pair", "paired", map[string]any{"host_id": "h1", "secret": "s"})

	select {
	case e := <-c.Events():
		if e.Topic != "daemon:pair" || e.Name != "paired" || e.Payload["host_id"] != "h1" || e.Payload["secret"] != "s" {
			t.Errorf("event = %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event")
	}
}

func TestPushWaitsForTheAnswerAndNeedsAJoinedTopic(t *testing.T) {
	f := newFakeServer(t)
	f.Handle = func(_ *fakeConn, _, event string, payload map[string]any) (string, map[string]any) {
		if event == "inventory" && payload["hostname"] == "rowan" {
			return "ok", map[string]any{}
		}
		return "error", map[string]any{"reason": "invalid_inventory"}
	}

	c := dial(t, f, nil)
	if _, err := c.Push(ctxTimeout(t), "daemon:host", "inventory", nil); err == nil {
		t.Error("a push on a topic that was not joined must fail")
	}

	if _, err := c.Join(ctxTimeout(t), "daemon:host", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Push(ctxTimeout(t), "daemon:host", "inventory", map[string]any{"hostname": "rowan"}); err != nil {
		t.Errorf("Push: %v", err)
	}
	if _, err := c.Push(ctxTimeout(t), "daemon:host", "inventory", map[string]any{"hostname": "bad"}); err == nil {
		t.Error("a refused push must fail")
	}
}

func TestTheHeartbeatIsSentWhileTheConnectionLives(t *testing.T) {
	previous := HeartbeatEvery
	HeartbeatEvery = 15 * time.Millisecond
	t.Cleanup(func() { HeartbeatEvery = previous })

	f := newFakeServer(t)
	dial(t, f, nil)

	eventually(t, "three heartbeats", func() bool { return f.Heartbeats() >= 3 })
}

func TestARefusedHandshakeIsADialErrorWithItsStatus(t *testing.T) {
	f := newFakeServer(t)
	f.Handshake = func(*http.Request) int { return http.StatusForbidden }

	_, err := Dial(ctxTimeout(t), f.URL(), nil)

	var refused *DialError
	if !errors.As(err, &refused) || refused.Status != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
}

func TestWhenTheServerDropsTheConnectionDoneIsClosedAndAWaitingJoinFails(t *testing.T) {
	f := newFakeServer(t)
	f.Join = func(c *fakeConn, _ string, _ map[string]any) (string, map[string]any) {
		c.Drop() // closes before answering
		return "ok", nil
	}

	c := dial(t, f, nil)
	_, err := c.Join(ctxTimeout(t), "daemon:host", nil)

	if err == nil {
		t.Fatal("a join that is never answered must fail when the connection ends")
	}
	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done was not closed")
	}
	if c.Err() == nil {
		t.Error("Err must say why it ended")
	}
}

func TestCloseEndsTheConnection(t *testing.T) {
	f := newFakeServer(t)
	c := dial(t, f, nil)

	c.Close()

	select {
	case <-c.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done was not closed")
	}
}
