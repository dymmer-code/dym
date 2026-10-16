package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeServer speaks enough of the Phoenix channels protocol (serializer 2.0.0) to test the client without Dymmer.
type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	// Handshake decides the HTTP status of the handshake; 0 or 101 accepts it.
	Handshake func(r *http.Request) int
	// Join answers a phx_join; the default accepts everything with an empty answer.
	Join func(c *fakeConn, topic string, payload map[string]any) (status string, response map[string]any)
	// Handle answers any other message of a joined topic; the default replies ok.
	Handle func(c *fakeConn, topic, event string, payload map[string]any) (status string, response map[string]any)
	// Connected runs when a connection has been accepted.
	Connected func(c *fakeConn)

	mu         sync.Mutex
	headers    []http.Header
	queries    []string
	joins      []fakeJoin
	heartbeats int
	conns      []*fakeConn
}

type fakeJoin struct {
	Topic   string
	Payload map[string]any
}

type fakeConn struct {
	ws      *websocket.Conn
	ctx     context.Context
	writeMu sync.Mutex

	refMu   sync.Mutex
	joinRef map[string]string
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// URL is the websocket endpoint, without the query.
func (f *fakeServer) URL() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/daemon/websocket"
}

func (f *fakeServer) Headers() []http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]http.Header(nil), f.headers...)
}

func (f *fakeServer) Queries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

func (f *fakeServer) Joins() []fakeJoin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeJoin(nil), f.joins...)
}

func (f *fakeServer) Heartbeats() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.heartbeats
}

// Last is the most recent connection, nil when there is none.
func (f *fakeServer) Last() *fakeConn {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.conns) == 0 {
		return nil
	}
	return f.conns[len(f.conns)-1]
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	if f.Handshake != nil {
		if status := f.Handshake(r); status != 0 && status != http.StatusSwitchingProtocols {
			http.Error(w, "refused", status)
			return
		}
	}

	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

	c := &fakeConn{ws: ws, ctx: r.Context(), joinRef: map[string]string{}}
	f.mu.Lock()
	f.headers = append(f.headers, r.Header.Clone())
	f.queries = append(f.queries, r.URL.RawQuery)
	f.conns = append(f.conns, c)
	f.mu.Unlock()

	if f.Connected != nil {
		go f.Connected(c)
	}

	for {
		_, data, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		f.dispatch(c, data)
	}
}

func (f *fakeServer) dispatch(c *fakeConn, data []byte) {
	var frame []json.RawMessage
	if json.Unmarshal(data, &frame) != nil || len(frame) != 5 {
		return
	}
	var joinRef, ref *string
	var topic, event string
	var payload map[string]any
	_ = json.Unmarshal(frame[0], &joinRef)
	_ = json.Unmarshal(frame[1], &ref)
	_ = json.Unmarshal(frame[2], &topic)
	_ = json.Unmarshal(frame[3], &event)
	_ = json.Unmarshal(frame[4], &payload)

	switch {
	case topic == "phoenix" && event == "heartbeat":
		f.mu.Lock()
		f.heartbeats++
		f.mu.Unlock()
		c.reply(nil, ref, "phoenix", "ok", map[string]any{})
	case event == "phx_join":
		f.mu.Lock()
		f.joins = append(f.joins, fakeJoin{Topic: topic, Payload: payload})
		f.mu.Unlock()
		status, response := "ok", map[string]any{}
		if f.Join != nil {
			status, response = f.Join(c, topic, payload)
		}
		if status == "ok" && joinRef != nil {
			c.refMu.Lock()
			c.joinRef[topic] = *joinRef
			c.refMu.Unlock()
		}
		c.reply(joinRef, ref, topic, status, response)
	default:
		status, response := "ok", map[string]any{}
		if f.Handle != nil {
			status, response = f.Handle(c, topic, event, payload)
		}
		c.reply(joinRef, ref, topic, status, response)
	}
}

func (c *fakeConn) write(frame []any) {
	data, _ := json.Marshal(frame)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.ws.Write(ctx, websocket.MessageText, data)
}

func (c *fakeConn) reply(joinRef, ref *string, topic, status string, response map[string]any) {
	var j, r any
	if joinRef != nil {
		j = *joinRef
	}
	if ref != nil {
		r = *ref
	}
	c.write([]any{j, r, topic, "phx_reply", map[string]any{"status": status, "response": response}})
}

// Push sends the client a message on a topic it has joined.
func (c *fakeConn) Push(topic, event string, payload map[string]any) {
	var j any
	c.refMu.Lock()
	if ref, ok := c.joinRef[topic]; ok {
		j = ref
	}
	c.refMu.Unlock()
	c.write([]any{j, nil, topic, event, payload})
}

// Drop closes the connection from the server side.
func (c *fakeConn) Drop() { _ = c.ws.Close(websocket.StatusGoingAway, "bye") }

// eventually waits for a condition without sleeping a fixed time.
func eventually(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
