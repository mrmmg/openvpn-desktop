package state

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// HistoryEntry records one connection session.
type HistoryEntry struct {
	Profile      string    `json:"profile"`
	Started      time.Time `json:"started"`
	Connected    time.Time `json:"connected,omitzero"`
	Ended        time.Time `json:"ended,omitzero"`
	Reason       string    `json:"reason,omitempty"`
	BytesIn      uint64    `json:"bytes_in,omitempty"`
	BytesOut     uint64    `json:"bytes_out,omitempty"`
	RemoteServer string    `json:"remote,omitempty"`
}

// Duration returns how long the tunnel was up (zero if it never connected).
func (e HistoryEntry) Duration() time.Duration {
	if e.Connected.IsZero() {
		return 0
	}
	end := e.Ended
	if end.IsZero() {
		end = time.Now()
	}
	return end.Sub(e.Connected)
}

// History is an append-only JSON-lines log of connection sessions.
type History struct {
	path string
	mu   sync.Mutex
	// MaxEntries bounds the file; older entries are dropped on compaction.
	MaxEntries int
}

// NewHistory returns a history stored at path.
func NewHistory(path string) *History {
	return &History{path: path, MaxEntries: 500}
}

// Append writes one entry.
func (h *History) Append(e HistoryEntry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	line, err := json.Marshal(e)
	if err != nil {
		_ = f.Close()
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		h.compactLocked()
	}
	return err
}

// List returns entries newest first. Malformed lines are skipped.
func (h *History) List() ([]HistoryEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries, err := h.readLocked()
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, nil
}

func (h *History) readLocked() ([]HistoryEntry, error) {
	f, err := os.Open(h.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []HistoryEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e HistoryEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

func (h *History) compactLocked() {
	if h.MaxEntries <= 0 {
		return
	}
	fi, err := os.Stat(h.path)
	// Cheap guard: only re-read when the file could plausibly exceed the bound.
	if err != nil || fi.Size() < int64(h.MaxEntries)*120 {
		return
	}
	entries, err := h.readLocked()
	if err != nil || len(entries) <= h.MaxEntries {
		return
	}
	entries = entries[len(entries)-h.MaxEntries:]
	var buf []byte
	for _, e := range entries {
		line, _ := json.Marshal(e)
		buf = append(append(buf, line...), '\n')
	}
	tmp := h.path + ".tmp"
	if os.WriteFile(tmp, buf, 0o600) == nil {
		_ = os.Rename(tmp, h.path)
	}
}

// Path returns the history file location.
func (h *History) Path() string { return h.path }
