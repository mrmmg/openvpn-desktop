package logs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// ReadTail returns up to n complete lines from the end of path, reading at
// most maxBytes.
func ReadTail(path string, n int, maxBytes int64) ([]string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := fi.Size()
	start := max(0, size-maxBytes)
	buf := make([]byte, size-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	if start > 0 {
		// Drop the partial first line.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	// Keep a trailing partial line out; the follower will deliver it.
	end := bytes.LastIndexByte(buf, '\n')
	consumed := size
	if end < len(buf)-1 {
		consumed = size - int64(len(buf)-end-1)
		buf = buf[:end+1]
	}
	lines := splitLines(buf)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, consumed, nil
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := bytes.Split(bytes.TrimSuffix(b, []byte("\n")), []byte("\n"))
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = string(bytes.TrimRight(p, "\r"))
	}
	return out
}

// Batch is a group of new lines delivered by Follow.
type Batch struct {
	Lines []string
	// Reset is true when the file was truncated or replaced; previously
	// delivered lines belong to an older session.
	Reset bool
	Err   error
}

// Follow polls path and sends new complete lines starting at offset. It
// handles truncation and replacement (rotation) and ends when ctx is done.
// Polling is used instead of inotify because the file is written by a root
// process and may be rotated; a short interval is cheap.
func Follow(ctx context.Context, path string, offset int64, interval time.Duration) <-chan Batch {
	ch := make(chan Batch, 16)
	go func() {
		defer close(ch)
		var (
			f       *os.File
			ino     uint64
			partial []byte
			lastErr error
		)
		defer func() {
			if f != nil {
				_ = f.Close()
			}
		}()
		send := func(b Batch) bool {
			select {
			case ch <- b:
				return true
			case <-ctx.Done():
				return false
			}
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			fi, err := os.Stat(path)
			switch {
			case err != nil:
				if lastErr == nil || err.Error() != lastErr.Error() {
					lastErr = err
					if !send(Batch{Err: err}) {
						return
					}
				}
			default:
				lastErr = nil
				curIno := inode(fi)
				reset := false
				if f == nil || curIno != ino {
					if f != nil {
						_ = f.Close()
						offset = 0
						reset = true
					}
					f, err = os.Open(path)
					if err != nil {
						f = nil
						break
					}
					ino = curIno
				}
				if fi.Size() < offset {
					offset, partial, reset = 0, nil, true
				}
				if fi.Size() > offset || reset {
					data := make([]byte, fi.Size()-offset)
					nr, _ := f.ReadAt(data, offset)
					data = data[:nr]
					offset += int64(nr)
					data = append(partial, data...)
					end := bytes.LastIndexByte(data, '\n')
					if end >= 0 {
						partial = append([]byte(nil), data[end+1:]...)
						data = data[:end+1]
					} else {
						partial, data = data, nil
					}
					if lines := splitLines(data); len(lines) > 0 || reset {
						if !send(Batch{Lines: lines, Reset: reset}) {
							return
						}
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return ch
}

func inode(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}
