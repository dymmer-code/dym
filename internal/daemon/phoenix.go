// Package daemon is the part of dym that runs on a host and reaches Dymmer by itself, instead of Dymmer reaching the
// host over SSH: it pairs with a one-time PIN, keeps one connection open, and tells Dymmer that the host is alive
// and what it is. It accepts no command from Dymmer.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Event is a message that the server pushed on a topic that was joined.
type Event struct {
	Topic   string
	Name    string
	Payload map[string]any
}

// JoinError is the answer of a server that refused to let the client join a topic.
type JoinError struct {
	Topic  string
	Reason string
}

func (e *JoinError) Error() string { return fmt.Sprintf("join %s refused: %s", e.Topic, e.Reason) }

// DialError is a handshake that the server rejected (a wrong secret, a deleted host, too many attempts).
type DialError struct {
	Status int
	Err    error
}

func (e *DialError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("the server refused the connection (HTTP %d)", e.Status)
	}
	return fmt.Sprintf("cannot connect: %v", e.Err)
}

func (e *DialError) Unwrap() error { return e.Err }

// Conn is a connection to a Phoenix socket (serializer 2.0.0: a frame is [join_ref, ref, topic, event, payload]).
// It sends the heartbeat the server expects, answers the pushes of the topics joined with Events, and ends when the
// connection does: Done is closed and Err says why.
type Conn struct {
	ws     *websocket.Conn
	cancel context.CancelFunc
	ctx    context.Context

	writeMu sync.Mutex
	refs    atomic.Uint64

	mu      sync.Mutex
	pending map[string]chan reply
	joined  map[string]string // topic -> join ref

	events    chan Event
	done      chan struct{}
	err       error
	heartbeat time.Duration // read once from HeartbeatEvery when the connection is made
}

type reply struct {
	status   string
	response map[string]any
}

// HeartbeatEvery is how often the heartbeat is sent. The server of Dymmer closes a connection that is silent for a
// minute, so this must stay well under that.
var HeartbeatEvery = 30 * time.Second

// Dial opens the connection. `url` is the websocket endpoint without the query (the version is added here) and
// `header` carries the credentials, which must not go in the URL because proxies write it in their logs.
func Dial(ctx context.Context, url string, header http.Header) (*Conn, error) {
	ws, resp, err := websocket.Dial(ctx, url+"?vsn=2.0.0", &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, &DialError{Status: status, Err: err}
	}

	ws.SetReadLimit(1 << 20)
	connCtx, cancel := context.WithCancel(context.Background())

	c := &Conn{
		ws:      ws,
		ctx:     connCtx,
		cancel:  cancel,
		pending: map[string]chan reply{},
		joined:  map[string]string{},
		events:  make(chan Event, 64),
		done:    make(chan struct{}),

		heartbeat: HeartbeatEvery,
	}

	go c.readLoop()
	go c.heartbeatLoop()
	return c, nil
}

// Events delivers what the server pushes on the topics that were joined. It is closed when the connection ends.
func (c *Conn) Events() <-chan Event { return c.events }

// Done is closed when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err says why the connection ended, once Done is closed.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection.
func (c *Conn) Close() {
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
	c.cancel()
}

// Join asks the server to join a topic and returns what it answered.
func (c *Conn) Join(ctx context.Context, topic string, payload map[string]any) (map[string]any, error) {
	ref := c.nextRef()

	c.mu.Lock()
	c.joined[topic] = ref
	c.mu.Unlock()

	r, err := c.request(ctx, ref, ref, topic, "phx_join", payload)
	if err != nil {
		return nil, err
	}
	if r.status != "ok" {
		c.mu.Lock()
		delete(c.joined, topic)
		c.mu.Unlock()
		reason, _ := r.response["reason"].(string)
		return nil, &JoinError{Topic: topic, Reason: reason}
	}
	return r.response, nil
}

// Push sends a message on a joined topic and waits for the answer of the server.
func (c *Conn) Push(ctx context.Context, topic, event string, payload map[string]any) (map[string]any, error) {
	c.mu.Lock()
	joinRef, ok := c.joined[topic]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("topic %s was not joined", topic)
	}

	r, err := c.request(ctx, joinRef, c.nextRef(), topic, event, payload)
	if err != nil {
		return nil, err
	}
	if r.status != "ok" {
		reason, _ := r.response["reason"].(string)
		return nil, fmt.Errorf("%s on %s refused: %s", event, topic, reason)
	}
	return r.response, nil
}

func (c *Conn) nextRef() string { return fmt.Sprintf("%d", c.refs.Add(1)) }

func (c *Conn) request(ctx context.Context, joinRef, ref, topic, event string, payload map[string]any) (reply, error) {
	wait := make(chan reply, 1)

	c.mu.Lock()
	c.pending[ref] = wait
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, ref)
		c.mu.Unlock()
	}()

	if err := c.send(ctx, joinRef, ref, topic, event, payload); err != nil {
		return reply{}, err
	}

	select {
	case r := <-wait:
		return r, nil
	case <-c.done:
		return reply{}, c.endError()
	case <-ctx.Done():
		return reply{}, ctx.Err()
	}
}

func (c *Conn) send(ctx context.Context, joinRef, ref, topic, event string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	var join any
	if joinRef != "" {
		join = joinRef
	}
	frame, err := json.Marshal([]any{join, ref, topic, event, payload})
	if err != nil {
		return err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, frame)
}

func (c *Conn) endError() error {
	if err := c.Err(); err != nil {
		return err
	}
	return errors.New("connection closed")
}

func (c *Conn) readLoop() {
	defer close(c.done)
	defer close(c.events)

	for {
		_, data, err := c.ws.Read(c.ctx)
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}

		var frame []json.RawMessage
		if json.Unmarshal(data, &frame) != nil || len(frame) != 5 {
			continue
		}

		var ref *string
		var topic, event string
		var payload map[string]any
		_ = json.Unmarshal(frame[1], &ref)
		_ = json.Unmarshal(frame[2], &topic)
		_ = json.Unmarshal(frame[3], &event)
		_ = json.Unmarshal(frame[4], &payload)

		switch event {
		case "phx_reply":
			c.deliverReply(ref, payload)
		case "phx_close", "phx_error":
			c.mu.Lock()
			delete(c.joined, topic)
			c.mu.Unlock()
			c.emit(Event{Topic: topic, Name: event, Payload: payload})
		default:
			c.emit(Event{Topic: topic, Name: event, Payload: payload})
		}
	}
}

func (c *Conn) deliverReply(ref *string, payload map[string]any) {
	if ref == nil {
		return
	}
	status, _ := payload["status"].(string)
	response, _ := payload["response"].(map[string]any)

	c.mu.Lock()
	wait := c.pending[*ref]
	c.mu.Unlock()
	if wait != nil {
		wait <- reply{status: status, response: response}
	}
}

func (c *Conn) emit(e Event) {
	select {
	case c.events <- e:
	default: // nobody is listening and the buffer is full: this client does not need the old ones
	}
}

func (c *Conn) heartbeatLoop() {
	ticker := time.NewTicker(c.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(c.ctx, c.heartbeat)
			err := c.send(ctx, "", c.nextRef(), "phoenix", "heartbeat", nil)
			cancel()
			if err != nil {
				c.Close()
				return
			}
		}
	}
}
