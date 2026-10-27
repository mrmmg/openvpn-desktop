package mgmt

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer scripts the OpenVPN side of a management connection.
type fakeServer struct {
	conn net.Conn
	r    *bufio.Reader
}

func newPair(t *testing.T) (*Client, *fakeServer) {
	t.Helper()
	a, b := net.Pipe()
	c := NewClient(a)
	t.Cleanup(func() { _ = c.Close(); _ = b.Close() })
	return c, &fakeServer{conn: b, r: bufio.NewReader(b)}
}

func (s *fakeServer) expect(t *testing.T, want string) {
	t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := s.r.ReadString('\n')
	if err != nil {
		t.Errorf("server read: %v", err)
		return
	}
	if got := strings.TrimSpace(line); got != want {
		t.Errorf("server got %q, want %q", got, want)
	}
}

func (s *fakeServer) send(lines ...string) {
	for _, l := range lines {
		_, _ = s.conn.Write([]byte(l + "\r\n"))
	}
}

func TestCommandsAndEvents(t *testing.T) {
	c, srv := newPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go func() {
		srv.send(">INFO:OpenVPN Management Interface Version 6")
		srv.expect(t, "pid")
		srv.send(">LOG:1700000000,I,interleaved log line", "SUCCESS: pid=4242")
		srv.expect(t, "state on all")
		srv.send("SUCCESS: real-time state notification set to ON", "1700000000,CONNECTING,,,,,,", "1700000001,CONNECTED,SUCCESS,10.8.0.6,203.0.113.7,1194,,,", "END")
		srv.expect(t, "bogus")
		srv.send("ERROR: unknown command [bogus]")
		srv.send(">STATE:1700000002,RECONNECTING,ping-restart,,,,,", ">BYTECOUNT:100,200")
	}()

	pid, err := c.PID(ctx)
	if err != nil || pid != 4242 {
		t.Fatalf("pid = %d %v", pid, err)
	}
	lines, err := c.Command(ctx, "state on all")
	if err != nil || len(lines) != 3 {
		t.Fatalf("state on all = %q %v", lines, err)
	}
	st, err := ParseState(lines[2])
	if err != nil || st.Name != "CONNECTED" || st.LocalIP != "10.8.0.6" || st.RemoteIP != "203.0.113.7" {
		t.Fatalf("state = %+v %v", st, err)
	}
	_, err = c.Command(ctx, "bogus")
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("want CommandError, got %v", err)
	}
	kinds := map[string]string{}
	for len(kinds) < 4 {
		select {
		case ev := <-c.Events():
			kinds[ev.Kind] = ev.Data
		case <-ctx.Done():
			t.Fatalf("events: got %v", kinds)
		}
	}
	if in, out, err := ParseByteCount(kinds[EventByteCount]); err != nil || in != 100 || out != 200 {
		t.Fatalf("bytecount %d %d %v", in, out, err)
	}
	if l := ParseLog(kinds[EventLog]); l.Flags != "I" || l.Message != "interleaved log line" {
		t.Fatalf("log = %+v", l)
	}
}

func TestSendCredentialsEscapes(t *testing.T) {
	c, srv := newPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		srv.expect(t, `username "Auth" "al\"ice"`)
		srv.send("SUCCESS: 'Auth' username entered, but not yet verified")
		srv.expect(t, `password "Auth" "p\\w\"d"`)
		srv.send("ERROR: password rejected")
	}()
	err := c.SendCredentials(ctx, "Auth", `al"ice`, `p\w"d`)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), `p\w`) {
		t.Fatalf("error leaks password: %v", err)
	}
	if _, err := c.Command(ctx, "a\nb"); err == nil {
		t.Fatal("multi-line commands must be rejected")
	}
}

func TestConnectionClose(t *testing.T) {
	c, srv := newPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		srv.expect(t, "pid")
		_ = srv.conn.Close()
	}()
	if _, err := c.PID(ctx); err == nil {
		t.Fatal("expected error when the server goes away")
	}
	select {
	case <-c.Done():
	case <-ctx.Done():
		t.Fatal("Done not closed")
	}
	if _, ok := <-c.Events(); ok {
		t.Fatal("events channel must be closed")
	}
}

func TestCommandTimeoutKeepsStreamAligned(t *testing.T) {
	c, srv := newPair(t)
	go func() {
		srv.expect(t, "version")
		time.Sleep(150 * time.Millisecond)
		srv.send("OpenVPN Version: 2.7", "END")
		srv.expect(t, "pid")
		srv.send("SUCCESS: pid=7")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err := c.Command(ctx, "version")
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline, got %v", err)
	}
	time.Sleep(250 * time.Millisecond) // late response is discarded
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	if pid, err := c.PID(ctx2); err != nil || pid != 7 {
		t.Fatalf("stream misaligned: %d %v", pid, err)
	}
}

func TestParsers(t *testing.T) {
	if !isMultiLine("state on all") || isMultiLine("state on") || !isMultiLine("log 20") || !isMultiLine("version") || isMultiLine("pid") || isMultiLine("bytecount 1") {
		t.Fatal("isMultiLine")
	}
	pr := ParsePassword("Need 'Auth' username/password")
	if pr.Type != "Auth" || !pr.NeedUsername {
		t.Fatalf("%+v", pr)
	}
	pr = ParsePassword("Need 'Private Key' password")
	if pr.Type != "Private Key" || pr.NeedUsername {
		t.Fatalf("%+v", pr)
	}
	pr = ParsePassword("Verification Failed: 'Auth'")
	if !pr.Failed || pr.Type != "Auth" {
		t.Fatalf("%+v", pr)
	}
	pr = ParsePassword("Need 'Auth' username/password SC:1,Enter PIN")
	if pr.Challenge != "1,Enter PIN" {
		t.Fatalf("%+v", pr)
	}
	if _, err := ParseState("garbage"); err == nil {
		t.Fatal("ParseState must fail")
	}
	if Quote(`a"b\c`) != `"a\"b\\c"` {
		t.Fatal(Quote(`a"b\c`))
	}
}
