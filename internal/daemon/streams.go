package daemon

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Stream is an operation that keeps sending until it is stopped: it hands lines to emit, in batches, and ends with a
// result that says why. Like the other operations it is fixed, and it only reads what the owner declared.
type Stream func(ctx context.Context, env OpEnv, args []string, emit func(string)) Result

// Streams are the operations of this kind.
func Streams() map[string]Stream {
	return map[string]Stream{"log_follow.sh": followLog}
}

const (
	followPoll    = 500 * time.Millisecond
	followInitial = 10        // lines shown first, as `tail -f` does
	followRead    = 256 << 10 // the most read from a file in one turn
	flushEvery    = 250 * time.Millisecond
	flushBytes    = 16 << 10
	maxStreamTime = time.Hour
)

// followLog is `log_follow.sh <file|journal> <path or unit> [filter]`: the lines that come to a log, with the filter, until
// it is stopped. A file is followed through truncations and rotations.
func followLog(ctx context.Context, env OpEnv, args []string, emit func(string)) Result {
	source, refused := parseLog(env, args)
	if refused != nil {
		return *refused
	}

	out := newBatcher(ctx, emit)
	defer out.stop()
	onLine := func(line string) {
		if source.matches(line) {
			out.add(line)
		}
	}

	if source.kind == "journal" {
		if env.Stream == nil {
			return fail("The journal cannot be followed.")
		}
		r := env.Stream(ctx, ExecSpec{Argv: []string{"journalctl", "-f", "--no-pager", "--output=cat", "--unit=" + source.target}}, onLine)
		if ctx.Err() != nil {
			return ok("")
		}
		return fail("The journal ended: " + r.Output)
	}
	return followFile(ctx, source.target, onLine)
}

func followFile(ctx context.Context, path string, onLine func(string)) Result {
	path = filepath.Clean(path)
	initial, readable := tailFile(path)
	if !readable {
		return fail("The file cannot be read.")
	}
	for _, line := range initial[max(len(initial)-followInitial, 0):] {
		onLine(line)
	}

	f, err := os.Open(path)
	if err != nil {
		return fail("The file cannot be read.")
	}
	defer func() { f.Close() }()
	info, _ := f.Stat()
	offset := info.Size()
	pending := ""

	ticker := time.NewTicker(followPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ok("")
		case <-ticker.C:
		}

		// Rotated: the path is another file now, so the new one is read from its start.
		if now, err := os.Stat(path); err == nil && !os.SameFile(info, now) {
			if reopened, err := os.Open(path); err == nil {
				f.Close()
				f, info, offset, pending = reopened, now, 0, ""
			}
		}
		current, err := f.Stat()
		if err != nil {
			return fail("The file cannot be read.")
		}
		if current.Size() < offset { // truncated
			offset, pending = 0, ""
		}
		if current.Size() == offset {
			continue
		}

		buf := make([]byte, min(current.Size()-offset, followRead))
		n, err := f.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			return fail("The file cannot be read.")
		}
		offset += int64(n)

		text := pending + string(buf[:n])
		lines := strings.Split(text, "\n")
		pending = lines[len(lines)-1] // what has no end of line yet waits for the next turn
		if len(pending) > maxLine {
			pending = pending[len(pending)-maxLine:]
		}
		for _, line := range lines[:len(lines)-1] {
			onLine(line)
		}
	}
}

// batcher gathers lines and hands them over together, every flushEvery or when they are many, so that a log that
// writes a lot does not become a message for every line.
type batcher struct {
	mu    sync.Mutex
	lines []string
	size  int
	emit  func(string)
	done  chan struct{}
	wg    sync.WaitGroup
}

func newBatcher(ctx context.Context, emit func(string)) *batcher {
	b := &batcher{emit: emit, done: make(chan struct{})}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		ticker := time.NewTicker(flushEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				b.flush()
			case <-b.done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return b
}

func (b *batcher) add(line string) {
	b.mu.Lock()
	b.lines = append(b.lines, line)
	b.size += len(line) + 1
	full := b.size >= flushBytes
	b.mu.Unlock()
	if full {
		b.flush()
	}
}

func (b *batcher) flush() {
	b.mu.Lock()
	lines := b.lines
	b.lines, b.size = nil, 0
	b.mu.Unlock()
	if len(lines) > 0 {
		b.emit(strings.Join(lines, "\n") + "\n")
	}
}

func (b *batcher) stop() {
	close(b.done)
	b.wg.Wait()
	b.flush()
}
