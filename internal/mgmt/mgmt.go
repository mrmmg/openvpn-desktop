// Package mgmt is a client for the OpenVPN management interface
// (unix-socket variant). It separates asynchronous notifications (lines
// starting with '>') from command responses and serialises commands.
//
// Protocol reference: OpenVPN doc/management-notes.txt. Observed on 2.7:
//   - single-line commands answer "SUCCESS: ..." or "ERROR: ...";
//   - multi-line commands (version, state, log N, "state on all",
//     "log on all") answer lines terminated by "END" (or a single ERROR).
package mgmt

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event is an asynchronous notification.
type Event struct {
	// Kind is the notification name without '>' and ':' (STATE, LOG, ...).
	Kind string
	// Data is everything after the first ':'.
	Data string
}

// Common event kinds.
const (
	EventState     = "STATE"
	EventLog       = "LOG"
	EventByteCount = "BYTECOUNT"
	EventPassword  = "PASSWORD"
	EventHold      = "HOLD"
	EventFatal     = "FATAL"
	EventInfo      = "INFO"
	EventInfoMsg   = "INFOMSG"
	EventNeedOK    = "NEED-OK"
	EventNeedStr   = "NEED-STR"
)

// ErrClosed is returned after the connection ended.
var ErrClosed = errors.New("management connection closed")

// CommandError is an "ERROR: ..." response.
type CommandError struct{ Msg string }

func (e *CommandError) Error() string { return "openvpn: " + e.Msg }

type pending struct {
	multi bool
	lines []string
	done  chan error
}

// Client is a management interface connection.
type Client struct {
	conn   net.Conn
	events chan Event

	cmdMu sync.Mutex // serialises commands

	mu      sync.Mutex
	current *pending
	closed  bool
	err     error
	done    chan struct{}
}

// Dial connects to a unix socket.
func Dial(ctx context.Context, socket string) (*Client, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// NewClient wraps an existing connection (used by tests).
func NewClient(conn net.Conn) *Client {
	c := &Client{conn: conn, events: make(chan Event, 1024), done: make(chan struct{})}
	go c.readLoop()
	return c
}

// Events returns the notification channel. It is closed when the connection
// ends. Consumers must drain it; when the buffer is full LOG and BYTECOUNT
// events are dropped rather than blocking the connection.
func (c *Client) Events() <-chan Event { return c.events }

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns the reason the connection ended.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close terminates the connection (OpenVPN keeps running).
func (c *Client) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.conn.Close()
}

func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.HasPrefix(line, ">") {
			kind, data, _ := strings.Cut(line[1:], ":")
			ev := Event{Kind: kind, Data: data}
			if kind == EventLog || kind == EventByteCount {
				select {
				case c.events <- ev:
				default: // drop noisy events under back-pressure
				}
			} else {
				select {
				case c.events <- ev:
				case <-time.After(2 * time.Second):
				}
			}
			continue
		}
		c.handleResponseLine(line)
	}
	err := sc.Err()
	if err == nil {
		err = ErrClosed
	}
	c.mu.Lock()
	if c.closed {
		err = ErrClosed
	}
	c.err = err
	if c.current != nil {
		c.current.done <- err
		c.current = nil
	}
	c.mu.Unlock()
	close(c.events)
	close(c.done)
}

func (c *Client) handleResponseLine(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.current
	if p == nil {
		return // unsolicited output
	}
	finish := func(err error) {
		p.done <- err
		c.current = nil
	}
	if len(p.lines) == 0 && strings.HasPrefix(line, "ERROR:") {
		finish(&CommandError{Msg: strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))})
		return
	}
	if !p.multi {
		p.lines = append(p.lines, line)
		finish(nil)
		return
	}
	if line == "END" {
		finish(nil)
		return
	}
	p.lines = append(p.lines, line)
}

// isMultiLine reports whether a command's response is terminated by END.
func isMultiLine(cmd string) bool {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return false
	}
	switch f[0] {
	case "version", "help", "status":
		return true
	case "state", "log", "echo":
		if len(f) == 1 {
			return true
		}
		if f[1] == "on" || f[1] == "off" {
			return len(f) == 3 && f[2] == "all"
		}
		return true // "state all", "log 20", ...
	}
	return false
}

// Command sends a command and returns the response lines (without END).
// Secrets in cmd are never echoed into errors.
func (c *Client) Command(ctx context.Context, cmd string) ([]string, error) {
	if strings.ContainsAny(cmd, "\r\n") {
		return nil, errors.New("management command must be a single line")
	}
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()

	p := &pending{multi: isMultiLine(cmd), done: make(chan error, 1)}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.current = p
	c.mu.Unlock()

	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetWriteDeadline(dl)
	} else {
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	}
	if _, err := c.conn.Write([]byte(cmd + "\n")); err != nil {
		c.mu.Lock()
		c.current = nil
		c.mu.Unlock()
		return nil, fmt.Errorf("management write: %w", err)
	}
	select {
	case err := <-p.done:
		return p.lines, err
	case <-ctx.Done():
		// The response may still arrive; drop it to keep the stream aligned.
		c.mu.Lock()
		if c.current == p {
			c.current = &pending{multi: p.multi, done: make(chan error, 1)}
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

// Quote escapes a value for the management protocol (double quotes with
// backslash escapes).
func Quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// SendCredentials answers a >PASSWORD:Need '<type>' username/password request.
func (c *Client) SendCredentials(ctx context.Context, typ, username, password string) error {
	if username != "" {
		if _, err := c.Command(ctx, "username "+Quote(typ)+" "+Quote(username)); err != nil {
			return fmt.Errorf("send username: %w", redactErr(err))
		}
	}
	if _, err := c.Command(ctx, "password "+Quote(typ)+" "+Quote(password)); err != nil {
		return fmt.Errorf("send password: %w", redactErr(err))
	}
	return nil
}

func redactErr(err error) error {
	var ce *CommandError
	if errors.As(err, &ce) {
		return &CommandError{Msg: "command rejected"}
	}
	return err
}

// Signal sends a signal (SIGTERM, SIGHUP, SIGUSR1, SIGUSR2) to OpenVPN.
func (c *Client) Signal(ctx context.Context, sig string) error {
	_, err := c.Command(ctx, "signal "+sig)
	return err
}

// PID returns the OpenVPN process id.
func (c *Client) PID(ctx context.Context) (int, error) {
	lines, err := c.Command(ctx, "pid")
	if err != nil {
		return 0, err
	}
	for _, l := range lines {
		if _, v, ok := strings.Cut(l, "pid="); ok {
			return strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return 0, errors.New("unexpected pid response")
}

// State is a parsed >STATE notification or state history line.
type State struct {
	Time        time.Time
	Name        string // CONNECTING, WAIT, AUTH, GET_CONFIG, ASSIGN_IP, ADD_ROUTES, CONNECTED, RECONNECTING, EXITING, RESOLVE, TCP_CONNECT, AUTH_PENDING
	Description string
	LocalIP     string
	RemoteIP    string
	RemotePort  string
	LocalAddr   string
	LocalPort   string
	LocalIPv6   string
}

// ParseState parses "time,state,desc,localip,remoteip,remoteport,localaddr,localport,localipv6".
func ParseState(s string) (State, error) {
	f := strings.Split(s, ",")
	if len(f) < 2 {
		return State{}, fmt.Errorf("malformed state %q", s)
	}
	ts, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return State{}, fmt.Errorf("malformed state time %q", f[0])
	}
	get := func(i int) string {
		if i < len(f) {
			return f[i]
		}
		return ""
	}
	return State{
		Time: time.Unix(ts, 0), Name: f[1], Description: get(2), LocalIP: get(3),
		RemoteIP: get(4), RemotePort: get(5), LocalAddr: get(6), LocalPort: get(7), LocalIPv6: get(8),
	}, nil
}

// ParseByteCount parses "in,out" from >BYTECOUNT.
func ParseByteCount(s string) (in, out uint64, err error) {
	a, b, ok := strings.Cut(s, ",")
	if !ok {
		return 0, 0, fmt.Errorf("malformed bytecount %q", s)
	}
	if in, err = strconv.ParseUint(strings.TrimSpace(a), 10, 64); err != nil {
		return 0, 0, err
	}
	if out, err = strconv.ParseUint(strings.TrimSpace(b), 10, 64); err != nil {
		return 0, 0, err
	}
	return in, out, nil
}

// PasswordRequest is a parsed >PASSWORD notification.
type PasswordRequest struct {
	// Type is the credential type, e.g. "Auth" or "Private Key".
	Type string
	// NeedUsername is true for username/password requests.
	NeedUsername bool
	// Failed is true for "Verification Failed" notifications.
	Failed bool
	// Challenge carries static-challenge text, if any.
	Challenge string
	Raw       string
}

// ParsePassword parses the data of a >PASSWORD notification.
func ParsePassword(data string) PasswordRequest {
	pr := PasswordRequest{Raw: data}
	if rest, ok := strings.CutPrefix(data, "Verification Failed: "); ok {
		pr.Failed = true
		pr.Type = strings.Trim(strings.Fields(rest + " ")[0], "'")
		if i := strings.Index(rest, "'"); i >= 0 {
			if j := strings.Index(rest[i+1:], "'"); j >= 0 {
				pr.Type = rest[i+1 : i+1+j]
			}
		}
		return pr
	}
	if rest, ok := strings.CutPrefix(data, "Need '"); ok {
		if j := strings.Index(rest, "'"); j >= 0 {
			pr.Type = rest[:j]
			tail := rest[j+1:]
			pr.NeedUsername = strings.Contains(tail, "username/password")
			if _, ch, ok := strings.Cut(tail, "SC:"); ok {
				pr.Challenge = ch
			}
		}
	}
	return pr
}

// LogLine is a parsed >LOG notification ("time,flags,message").
type LogLine struct {
	Time    time.Time
	Flags   string // I info, F fatal, N non-fatal error, W warning, D debug
	Message string
}

// ParseLog parses a management log line.
func ParseLog(s string) LogLine {
	parts := strings.SplitN(s, ",", 3)
	if len(parts) < 3 {
		return LogLine{Message: s}
	}
	ts, _ := strconv.ParseInt(parts[0], 10, 64)
	return LogLine{Time: time.Unix(ts, 0), Flags: parts[1], Message: parts[2]}
}
