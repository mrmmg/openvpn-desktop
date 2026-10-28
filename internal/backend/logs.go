package backend

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/applog"
	"github.com/mrmmg/openvpn-desktop/internal/logs"
)

const (
	logCapacity  = 2000
	logFlushTick = 250 * time.Millisecond
)

// LogBuffer is a bounded ring of log entries. OpenVPN's log file (written
// to disk with owner-only permissions) is followed live; application
// events are added to the same view. Memory use is capped at logCapacity.
type LogBuffer struct {
	mu      sync.Mutex
	entries []LogEntry
	next    int64
	pending []LogEntry
	emit    Emit
}

// NewLogBuffer creates an empty buffer.
func NewLogBuffer(emit Emit) *LogBuffer { return &LogBuffer{emit: emit} }

// Add appends an entry (the message is redacted first).
func (b *LogBuffer) Add(source, level, ts, msg string) {
	msg = strings.TrimRight(applog.RedactString(msg), "\r\n")
	if ts == "" {
		ts = time.Now().Format(time.RFC3339)
	}
	b.mu.Lock()
	b.next++
	e := LogEntry{ID: b.next, Time: ts, Level: level, Source: source, Message: msg}
	b.entries = append(b.entries, e)
	if len(b.entries) > logCapacity {
		b.entries = append([]LogEntry(nil), b.entries[len(b.entries)-logCapacity*3/4:]...)
	}
	b.pending = append(b.pending, e)
	b.mu.Unlock()
}

// AddOpenVPN classifies and adds a raw OpenVPN log line.
func (b *LogBuffer) AddOpenVPN(line string) {
	ts, msg := logs.SplitTimestamp(line)
	b.Add("openvpn", levelOf(logs.Classify(line)), ts, msg)
}

func levelOf(c logs.Class) string {
	switch c {
	case logs.Error, logs.Auth, logs.TLS:
		return "error"
	case logs.Warning:
		return "warn"
	case logs.Debug:
		return "debug"
	}
	return "info"
}

// Snapshot returns the current entries.
func (b *LogBuffer) Snapshot() []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]LogEntry(nil), b.entries...)
}

// Clear empties the view (the log file on disk is untouched).
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	b.entries, b.pending = nil, nil
	b.mu.Unlock()
	b.emit(EventLogsClr, nil)
}

// Flush sends the entries added since the last flush as one batch.
func (b *LogBuffer) Flush() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	b.mu.Unlock()
	if len(batch) > 0 {
		b.emit(EventLogs, batch)
	}
}

// LogFollower feeds the buffer from the manager's current log file and
// flushes batches to the frontend a few times per second.
type LogFollower struct {
	core *Core
	buf  *LogBuffer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewLogFollower starts following.
func NewLogFollower(core *Core, buf *LogBuffer) *LogFollower {
	ctx, cancel := context.WithCancel(context.Background())
	f := &LogFollower{core: core, buf: buf, ctx: ctx, cancel: cancel}
	f.wg.Add(1)
	go f.run()
	return f
}

// Shutdown stops following.
func (f *LogFollower) Shutdown() {
	f.cancel()
	f.wg.Wait()
}

func (f *LogFollower) run() {
	defer f.wg.Done()
	tick := time.NewTicker(logFlushTick)
	defer tick.Stop()
	var (
		path string
		stop = func() {}
		in   <-chan logs.Batch
	)
	defer func() { stop() }()
	for {
		select {
		case <-f.ctx.Done():
			return
		case b, open := <-in:
			if !open {
				in = nil
				continue
			}
			if b.Reset {
				f.buf.Add("app", "info", "", "— log file restarted —")
			}
			for _, l := range b.Lines {
				f.buf.AddOpenVPN(l)
			}
		case <-tick.C:
			f.buf.Flush()
			if p := f.core.VPN.Status().LogPath; p != "" && p != path {
				stop()
				path = p
				ctx, cancel := context.WithCancel(f.ctx)
				stop = cancel
				in = logs.Follow(ctx, path, 0, 400*time.Millisecond)
				f.buf.Add("app", "info", "", "Following OpenVPN log: "+path)
			}
		}
	}
}
