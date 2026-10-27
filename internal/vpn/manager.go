package vpn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mrmmg/openvpn-wails/internal/analyzer"
	"github.com/mrmmg/openvpn-wails/internal/credentials"
	"github.com/mrmmg/openvpn-wails/internal/logs"
	"github.com/mrmmg/openvpn-wails/internal/mgmt"
	"github.com/mrmmg/openvpn-wails/internal/network"
	"github.com/mrmmg/openvpn-wails/internal/ovpn"
	"github.com/mrmmg/openvpn-wails/internal/process"
	"github.com/mrmmg/openvpn-wails/internal/profile"
	"github.com/mrmmg/openvpn-wails/internal/state"
)

// Errors returned by the manager.
var (
	ErrAlreadyRunning = errors.New("a connection managed by openvpn-wails is already active")
	ErrNotRunning     = errors.New("no OpenVPN connection to stop")
	ErrStopTimeout    = errors.New("OpenVPN did not exit in time")
	ErrNoPrompt       = errors.New("OpenVPN is not waiting for credentials")
)

// Launcher starts and signals processes with the required privileges.
type Launcher interface {
	Effective() string
	Interactive() bool
	Launch(ctx context.Context, s process.LaunchSpec) error
	InteractiveLaunchCmd(s process.LaunchSpec) *exec.Cmd
	Signal(ctx context.Context, p process.Proc, sig string) error
	Wrap(argv []string) []string
}

// ProcessTable reads the process list.
type ProcessTable interface {
	FindOpenVPN() ([]process.Proc, error)
	Inspect(pid int) (process.Proc, bool)
	Alive(pid int, startTicks uint64) bool
}

// Dialer connects to a management socket.
type Dialer func(ctx context.Context, socket string) (*mgmt.Client, error)

// Options configures the manager.
type Options struct {
	Binary         string
	Verbosity      int
	LogDir         string
	RuntimeDir     string
	CredExt        string
	ConnectTimeout time.Duration
	StopTimeout    time.Duration
	AutoReconnect  bool
	RetryInterval  time.Duration
	MaxRetries     int
}

// Manager controls one OpenVPN connection and observes others.
type Manager struct {
	opts    Options
	launch  Launcher
	procs   ProcessTable
	net     network.Provider
	dial    Dialer
	history *state.History
	log     *slog.Logger
	now     func() time.Time

	mu        sync.Mutex
	st        Status
	client    *mgmt.Client
	inst      *process.Instance
	stopping  bool
	attaching bool
	session   *state.HistoryEntry
	logRing   []string
	lastPlan  *StartPlan
	retries   int
	closed    bool

	updates chan struct{}
	notices chan Notice
	bg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
}

// New creates a manager.
func New(opts Options, l Launcher, procs ProcessTable, netp network.Provider, dial Dialer, hist *state.History, log *slog.Logger) *Manager {
	if dial == nil {
		dial = mgmt.Dial
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		opts: opts, launch: l, procs: procs, net: netp, dial: dial, history: hist, log: log,
		now:     time.Now,
		updates: make(chan struct{}, 1),
		notices: make(chan Notice, 32),
		ctx:     ctx, cancel: cancel,
	}
	m.st = Status{Phase: Disconnected, PhaseSince: m.now()}
	return m
}

// SocketPath is the management socket of the managed instance.
func (m *Manager) SocketPath() string { return filepath.Join(m.opts.RuntimeDir, "mgmt.sock") }

func (m *Manager) instancePath() string { return filepath.Join(m.opts.RuntimeDir, "instance.json") }

// LogPathFor returns the OpenVPN log file used for a profile.
func (m *Manager) LogPathFor(p profile.Profile) string {
	return filepath.Join(m.opts.LogDir, p.BaseName()+".log")
}

// Updates delivers a value whenever the status changed.
func (m *Manager) Updates() <-chan struct{} { return m.updates }

// Notices delivers notable events.
func (m *Manager) Notices() <-chan Notice { return m.notices }

// Status returns the current status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.copyStatusLocked()
}

func (m *Manager) copyStatusLocked() Status {
	s := m.st
	s.External = append([]process.Proc(nil), m.st.External...)
	s.Servers = append([]process.Proc(nil), m.st.Servers...)
	s.OverrideArgs = append([]string(nil), m.st.OverrideArgs...)
	return s
}

// SessionLog returns the most recent OpenVPN log lines of the managed
// session (from the management log cache).
func (m *Manager) SessionLog() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.logRing...)
}

func (m *Manager) changed() {
	select {
	case m.updates <- struct{}{}:
	default:
	}
}

func (m *Manager) notice(kind NoticeKind, title, msg string) {
	select {
	case m.notices <- Notice{Kind: kind, Title: title, Message: msg, At: m.now()}:
	default:
	}
}

func (m *Manager) setPhaseLocked(p Phase, detail string) {
	if m.st.Phase != p {
		m.st.PhaseSince = m.now()
	}
	m.st.Phase = p
	m.st.Detail = detail
}

// ---------------------------------------------------------------------------
// Startup / re-attach

// Init re-attaches to an OpenVPN process started by a previous session
// and scans for other OpenVPN processes. It never starts or stops anything.
func (m *Manager) Init(ctx context.Context) error {
	inst, err := process.LoadInstance(m.instancePath())
	if err != nil {
		m.log.Warn("ignoring instance record", "err", err)
		process.RemoveInstance(m.instancePath())
	}
	if inst != nil {
		if m.procs.Alive(inst.PID, inst.StartTicks) {
			m.mu.Lock()
			m.inst = inst
			m.st.Source = SourceManaged
			m.st.Profile = inst.Profile
			m.st.ProfilePath = inst.ProfilePath
			m.st.PID = inst.PID
			m.st.LogPath = inst.LogPath
			m.st.OverrideArgs = inst.Overrides
			m.setPhaseLocked(Unknown, "re-attaching")
			m.session = &state.HistoryEntry{Profile: inst.Profile, Started: inst.StartedAt}
			m.mu.Unlock()
			if err := m.attach(ctx, inst.Socket); err != nil {
				m.log.Warn("re-attach failed", "pid", inst.PID, "err", err)
				m.mu.Lock()
				m.st.Message = "OpenVPN is running but its management socket is unreachable: " + err.Error()
				m.mu.Unlock()
			}
		} else {
			m.log.Info("previous OpenVPN instance is gone", "pid", inst.PID)
			process.RemoveInstance(m.instancePath())
			if lines, _, err := logs.ReadTail(inst.LogPath, 300, 256<<10); err == nil {
				if f := logs.FindFailure(lines); f != nil && !strings.HasPrefix(f.Summary, "Stopped") {
					m.mu.Lock()
					m.st.Failure = f
					m.st.Profile = inst.Profile
					m.mu.Unlock()
				}
			}
		}
	}
	_, err = m.Refresh(ctx)
	return err
}

// ---------------------------------------------------------------------------
// Start

// StartPlan is a prepared launch.
type StartPlan struct {
	Profile   profile.Profile
	Overrides state.Overrides
	Spec      process.LaunchSpec
	Creds     credentials.Plan
	// Interactive is true when the launch needs the terminal (pkexec).
	Interactive bool
}

// PrepareStart builds the launch for a profile and prepares log/socket files.
func (m *Manager) PrepareStart(p profile.Profile, ov state.Overrides) (*StartPlan, error) {
	return m.PrepareStartWith(p, ov, false)
}

// PrepareStartWith is PrepareStart; with askCredentials a saved credential
// file is ignored and OpenVPN asks through the management interface.
func (m *Manager) PrepareStartWith(p profile.Profile, ov state.Overrides, askCredentials bool) (*StartPlan, error) {
	m.mu.Lock()
	busy := m.st.Source == SourceManaged && m.st.Phase != Disconnected && m.st.Phase != Failed
	binary, verbosity := m.opts.Binary, m.opts.Verbosity
	m.mu.Unlock()
	if busy {
		return nil, ErrAlreadyRunning
	}
	cfg, err := ovpn.ParseFile(p.Path)
	if err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	creds := credentials.MakePlan(cfg, p.Path, m.opts.CredExt)
	var extra []string
	if askCredentials && (creds.Source == credentials.SavedFile || creds.Source == credentials.ConfigFile) {
		if creds.Source == credentials.ConfigFile {
			// A later bare --auth-user-pass overrides the file named in the profile.
			extra = []string{"--auth-user-pass"}
		}
		creds = credentials.Plan{Source: credentials.Prompt}
	}
	spec := process.LaunchSpec{
		Binary:      binary,
		ProfilePath: p.Path,
		Dir:         p.Dir,
		LogPath:     m.LogPathFor(p),
		Socket:      m.SocketPath(),
		Verbosity:   verbosity,
		ExtraArgs:   append(analyzer.OverrideArgs(ov), extra...),
	}
	if creds.Source == credentials.SavedFile {
		spec.CredFile = creds.Path
	}
	if creds.Source == credentials.Prompt || creds.PasswordPrompt {
		spec.InteractiveAuth = true
	}
	if m.launch.Effective() != "none" {
		if u, err := user.Current(); err == nil {
			spec.ClientUser = u.Username
		}
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(spec.Binary); err != nil {
		return nil, fmt.Errorf("OpenVPN binary not found at %s (set openvpn.binary in the config)", spec.Binary)
	}
	if err := process.PrepareFiles(spec); err != nil {
		return nil, err
	}
	plan := &StartPlan{Profile: p, Overrides: ov, Spec: spec, Creds: creds, Interactive: m.launch.Interactive()}
	m.mu.Lock()
	m.lastPlan = plan
	m.st = Status{
		Phase: Connecting, PhaseSince: m.now(), Detail: "starting OpenVPN", Source: SourceManaged,
		Profile: p.Name, ProfilePath: p.Path, LogPath: spec.LogPath, OverrideArgs: spec.ExtraArgs,
		External: m.st.External, Reconnects: m.st.Reconnects,
	}
	m.logRing = nil
	m.stopping = false
	m.session = &state.HistoryEntry{Profile: p.Name, Started: m.now()}
	m.mu.Unlock()
	m.changed()
	return plan, nil
}

// Launch runs a prepared plan non-interactively. It returns
// process.ErrAuthRequired when the user must authenticate first.
func (m *Manager) Launch(ctx context.Context, plan *StartPlan) error {
	err := m.launch.Launch(ctx, plan.Spec)
	switch {
	case err == nil, errors.Is(err, process.ErrAuthRequired):
	case errors.Is(err, process.ErrCancelled):
		// Not a failure: the user changed their mind.
		m.AbortStart(err.Error())
	default:
		m.failStart(err)
	}
	return err
}

// InteractiveLaunchCmd returns the command to run with the terminal.
func (m *Manager) InteractiveLaunchCmd(plan *StartPlan) *exec.Cmd {
	return m.launch.InteractiveLaunchCmd(plan.Spec)
}

// AbortStart resets the state after a cancelled launch.
func (m *Manager) AbortStart(reason string) {
	m.mu.Lock()
	if m.st.Source == SourceManaged && m.client == nil {
		m.setPhaseLocked(Disconnected, "")
		m.st.Source = SourceNone
		m.st.Message = reason
		m.session = nil
	}
	m.mu.Unlock()
	m.changed()
}

func (m *Manager) failStart(err error) {
	m.mu.Lock()
	plan := m.lastPlan
	m.mu.Unlock()
	f := &logs.Failure{Summary: "Failed to start OpenVPN", Hint: "", Line: err.Error()}
	if plan != nil {
		if lines, _, rerr := logs.ReadTail(plan.Spec.LogPath, 200, 128<<10); rerr == nil {
			if lf := logs.FindFailure(lines); lf != nil {
				f = lf
				f.Summary = "Failed to start OpenVPN: " + lf.Summary
			}
		}
	}
	m.mu.Lock()
	m.setPhaseLocked(Failed, "")
	m.st.Failure = f
	m.endSessionLocked(f.Summary)
	m.mu.Unlock()
	m.notice(NoticeError, "OpenVPN failed to start", f.Summary)
	m.changed()
}

// FinishStart waits for the management socket of a launched OpenVPN and
// attaches to it.
func (m *Manager) FinishStart(ctx context.Context, plan *StartPlan) error {
	deadline := m.now().Add(15 * time.Second)
	var lastErr error
	for {
		if _, err := os.Stat(plan.Spec.Socket); err == nil {
			if err := m.attach(ctx, plan.Spec.Socket); err == nil {
				break
			} else {
				lastErr = err
			}
		}
		// Early exit: OpenVPN logged a fatal error before creating the socket.
		if lines, _, err := logs.ReadTail(plan.Spec.LogPath, 50, 64<<10); err == nil {
			for _, l := range lines {
				if strings.Contains(l, "Exiting due to fatal error") || strings.Contains(l, "Options error") {
					err := errors.New("OpenVPN exited during startup")
					m.failStart(err)
					return err
				}
			}
		}
		if m.now().After(deadline) {
			if lastErr == nil {
				lastErr = errors.New("management socket did not appear")
			}
			m.failStart(lastErr)
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	m.mu.Lock()
	pid := m.st.PID
	m.mu.Unlock()
	inst := process.Instance{
		Profile: plan.Profile.Name, ProfilePath: plan.Profile.Path, PID: pid,
		Socket: plan.Spec.Socket, LogPath: plan.Spec.LogPath, StartedAt: m.now(),
		Overrides: plan.Spec.ExtraArgs,
	}
	if p, ok := m.procs.Inspect(pid); ok {
		inst.StartTicks = p.StartTicks
	}
	if err := process.SaveInstance(m.instancePath(), inst); err != nil {
		m.log.Warn("cannot save instance record", "err", err)
	}
	m.mu.Lock()
	m.inst = &inst
	m.mu.Unlock()
	m.log.Info("OpenVPN started", "profile", plan.Profile.Name, "pid", pid)
	go m.watchConnectTimeout(plan)
	return nil
}

// watchConnectTimeout reports when the tunnel is not up within the timeout.
func (m *Manager) watchConnectTimeout(plan *StartPlan) {
	timeout := m.opts.ConnectTimeout
	if timeout <= 0 {
		return
	}
	select {
	case <-m.ctx.Done():
		return
	case <-time.After(timeout):
	}
	m.mu.Lock()
	stillConnecting := m.st.Phase == Connecting && m.st.Profile == plan.Profile.Name && m.st.Password == nil
	if stillConnecting {
		m.st.Message = fmt.Sprintf("Not connected after %s — OpenVPN keeps retrying; check the log or stop the connection.", timeout)
	}
	m.mu.Unlock()
	if stillConnecting {
		m.notice(NoticeInfo, "Still connecting", "No connection after "+timeout.String())
		m.changed()
	}
}

// ---------------------------------------------------------------------------
// Management attachment

func (m *Manager) attach(ctx context.Context, socket string) error {
	m.mu.Lock()
	if m.attaching || m.client != nil {
		m.mu.Unlock()
		return errors.New("already attached")
	}
	m.attaching = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.attaching = false
		m.mu.Unlock()
	}()
	dctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	c, err := m.dial(dctx, socket)
	if err != nil {
		return err
	}
	cctx, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	pid, err := c.PID(cctx)
	if err != nil {
		_ = c.Close()
		return fmt.Errorf("management handshake: %w", err)
	}
	history, err := c.Command(cctx, "state on all")
	if err != nil {
		_ = c.Close()
		return fmt.Errorf("management handshake: %w", err)
	}
	logHist, err := c.Command(cctx, "log on all")
	if err != nil {
		m.log.Warn("log on all failed", "err", err)
	}
	if _, err := c.Command(cctx, "bytecount 1"); err != nil {
		m.log.Debug("bytecount failed", "err", err)
	}
	m.mu.Lock()
	m.client = c
	m.st.Attached = true
	m.st.PID = pid
	m.st.Source = SourceManaged
	for _, l := range logHist {
		if strings.HasPrefix(l, "SUCCESS:") {
			continue
		}
		m.handleLogLocked(mgmt.ParseLog(l))
	}
	for _, l := range history {
		if strings.HasPrefix(l, "SUCCESS:") {
			continue
		}
		if s, err := mgmt.ParseState(l); err == nil {
			m.handleStateLocked(s, false)
		}
	}
	m.mu.Unlock()
	m.changed()
	m.bg.Add(1)
	go m.eventLoop(c)
	// OpenVPN re-sends a pending >PASSWORD request to a newly connected
	// client, so nothing else is needed to pick up an outstanding prompt.
	return nil
}

var ifaceRe = regexp.MustCompile(`(?:TUN/TAP|DCO) device \[?([^\s\]]+)\]? opened|net_iface_up: set (\S+) up|Opened utun device (\S+)`)

func (m *Manager) eventLoop(c *mgmt.Client) {
	defer m.bg.Done()
	for ev := range c.Events() {
		m.handleEvent(c, ev)
	}
	m.onDetach(c)
}

func (m *Manager) handleEvent(c *mgmt.Client, ev mgmt.Event) {
	m.mu.Lock()
	switch ev.Kind {
	case mgmt.EventState:
		if s, err := mgmt.ParseState(ev.Data); err == nil {
			m.handleStateLocked(s, true)
		}
	case mgmt.EventLog:
		m.handleLogLocked(mgmt.ParseLog(ev.Data))
	case mgmt.EventByteCount:
		if in, out, err := mgmt.ParseByteCount(ev.Data); err == nil {
			m.st.BytesIn, m.st.BytesOut = in, out
			if m.session != nil {
				m.session.BytesIn, m.session.BytesOut = in, out
			}
		}
		m.mu.Unlock()
		return // byte counts alone do not need a redraw of everything
	case mgmt.EventPassword:
		pr := mgmt.ParsePassword(ev.Data)
		if pr.Failed {
			m.st.Failure = &logs.Failure{Summary: "Authentication failed", Hint: "Enter the correct credentials.", Auth: true, Line: ev.Data}
			m.st.Password = nil
			profileName := m.st.Profile
			m.mu.Unlock()
			m.notice(NoticeAuthFailed, "VPN authentication failed", profileName)
			m.changed()
			return
		}
		if pr.Type != "" {
			m.st.Password = &pr
			m.setPhaseLocked(Connecting, "waiting for credentials")
		}
	case mgmt.EventFatal:
		m.st.Failure = &logs.Failure{Summary: "OpenVPN fatal error", Line: ev.Data}
	case mgmt.EventHold:
		// Only happens if the profile itself enables management-hold.
		go func() {
			ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
			defer cancel()
			_, _ = c.Command(ctx, "hold release")
		}()
	case mgmt.EventInfoMsg, mgmt.EventInfo:
		if strings.Contains(ev.Data, "WEB_AUTH") || strings.Contains(ev.Data, "OPEN_URL") {
			url := ev.Data[strings.LastIndex(ev.Data, ":")+1:]
			if i := strings.Index(ev.Data, "http"); i >= 0 {
				url = ev.Data[i:]
			}
			m.st.Message = "Web authentication required: open " + url
		}
	case mgmt.EventNeedOK, mgmt.EventNeedStr:
		m.st.Message = "OpenVPN requests input: " + ev.Data
	}
	m.mu.Unlock()
	m.changed()
}

func (m *Manager) handleStateLocked(s mgmt.State, live bool) {
	switch s.Name {
	case "CONNECTED":
		first := m.st.Phase != Connected
		m.setPhaseLocked(Connected, s.Description)
		m.st.ConnectedSince = s.Time
		m.st.LocalIP, m.st.LocalIPv6 = s.LocalIP, s.LocalIPv6
		m.st.RemoteIP, m.st.RemotePort = s.RemoteIP, s.RemotePort
		m.st.Password = nil
		if strings.HasPrefix(s.Description, "SUCCESS") {
			m.st.Failure = nil
			m.st.Message = ""
		} else if s.Description != "" {
			m.st.Message = "Connected with errors (" + s.Description + "); see the log."
		}
		m.retries = 0
		if m.session != nil {
			if m.session.Connected.IsZero() {
				m.session.Connected = s.Time
			}
			m.session.RemoteServer = s.RemoteIP
		}
		if live && first {
			m.notice(NoticeConnected, "VPN connected", m.st.Profile)
		}
	case "RECONNECTING":
		m.setPhaseLocked(Connecting, "reconnecting ("+s.Description+")")
		m.st.Reconnects++
		if live {
			m.notice(NoticeReconnecting, "VPN reconnecting", s.Description)
		}
	case "EXITING":
		m.setPhaseLocked(Disconnecting, s.Description)
	default:
		detail := s.Name
		if s.Description != "" {
			detail += " (" + s.Description + ")"
		}
		if m.st.Password == nil || s.Name != "AUTH" {
			m.setPhaseLocked(Connecting, strings.ToLower(detail))
		}
	}
}

func (m *Manager) handleLogLocked(l mgmt.LogLine) {
	line := l.Message
	if !l.Time.IsZero() {
		line = l.Time.Format("2006-01-02 15:04:05") + " " + line
	}
	m.logRing = append(m.logRing, line)
	if len(m.logRing) > 3000 {
		m.logRing = append([]string(nil), m.logRing[len(m.logRing)-2000:]...)
	}
	if mm := ifaceRe.FindStringSubmatch(l.Message); mm != nil {
		name := mm[1] + mm[2] + mm[3]
		m.st.Iface = strings.TrimPrefix(name, "/dev/")
	}
}

// onDetach runs when the management connection ends.
func (m *Manager) onDetach(c *mgmt.Client) {
	m.mu.Lock()
	if m.client != c {
		m.mu.Unlock()
		return
	}
	m.client = nil
	m.st.Attached = false
	inst := m.inst
	stopping := m.stopping
	m.mu.Unlock()

	// Give the process a moment to exit after closing the socket.
	alive := false
	if inst != nil {
		for i := 0; i < 15; i++ {
			if alive = m.procs.Alive(inst.PID, inst.StartTicks); !alive {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		m.mu.Lock()
		pid := m.st.PID
		m.mu.Unlock()
		alive = pid > 0 && m.procs.Alive(pid, 0)
	}
	if alive {
		m.mu.Lock()
		m.st.Message = "Lost the management connection; OpenVPN is still running."
		m.mu.Unlock()
		m.changed()
		return
	}
	m.processGone(stopping)
}

// processGone finalises a managed session whose process has exited.
func (m *Manager) processGone(stopping bool) {
	m.mu.Lock()
	// Already finalised (onDetach and Refresh can both notice the exit).
	if m.st.Source != SourceManaged || m.closed || (m.inst == nil && m.st.PID == 0) {
		m.mu.Unlock()
		return
	}
	ring := append([]string(nil), m.logRing...)
	logPath := m.st.LogPath
	wasConnected := m.st.Phase == Connected
	m.mu.Unlock()

	if lines, _, err := logs.ReadTail(logPath, 300, 256<<10); err == nil && len(lines) > 0 {
		ring = lines
	}
	failure := logs.FindFailure(ring)
	process.RemoveInstance(m.instancePath())

	m.mu.Lock()
	m.inst = nil
	m.st.PID = 0
	m.st.Iface = ""
	m.st.Password = nil
	m.st.Attached = false
	m.st.LocalIP, m.st.LocalIPv6, m.st.RemoteIP, m.st.RemotePort = "", "", "", ""
	m.st.ConnectedSince = time.Time{}
	m.st.BytesIn, m.st.BytesOut = 0, 0
	reason := "stopped by user"
	if stopping {
		m.setPhaseLocked(Disconnected, "")
		m.st.Failure = nil
		m.st.Source = SourceNone
	} else {
		if failure == nil || strings.HasPrefix(failure.Summary, "Stopped") {
			failure = &logs.Failure{Summary: "OpenVPN exited unexpectedly", Hint: "See the log for details."}
			if f2 := logs.FindFailure(ring); f2 != nil && strings.HasPrefix(f2.Summary, "Stopped") {
				failure = &logs.Failure{Summary: "OpenVPN was terminated outside openvpn-wails", Line: f2.Line}
			}
		}
		m.setPhaseLocked(Failed, "")
		m.st.Failure = failure
		reason = failure.Summary
	}
	m.endSessionLocked(reason)
	plan := m.lastPlan
	auto := !stopping && m.opts.AutoReconnect && plan != nil && !failure.Auth && m.retries < m.opts.MaxRetries && !m.closed
	if auto {
		m.retries++
		m.st.Message = fmt.Sprintf("Reconnecting in %s (attempt %d/%d)…", m.opts.RetryInterval, m.retries, m.opts.MaxRetries)
	}
	m.mu.Unlock()

	switch {
	case stopping:
		m.notice(NoticeDisconnected, "VPN disconnected", "")
	case wasConnected:
		m.notice(NoticeDropped, "VPN connection dropped", failure.Summary)
	default:
		if failure.Auth {
			m.notice(NoticeAuthFailed, "VPN authentication failed", failure.Summary)
		} else {
			m.notice(NoticeError, "VPN failed", failure.Summary)
		}
	}
	m.changed()
	if auto {
		m.bg.Add(1)
		go m.autoReconnect(plan)
	}
}

func (m *Manager) autoReconnect(plan *StartPlan) {
	defer m.bg.Done()
	m.mu.Lock()
	wait := m.opts.RetryInterval
	m.mu.Unlock()
	select {
	case <-m.ctx.Done():
		return
	case <-time.After(wait):
	}
	m.mu.Lock()
	idle := m.st.Phase == Failed && m.st.Source == SourceManaged
	m.mu.Unlock()
	if !idle {
		return
	}
	retries := m.retries
	p, err := m.PrepareStart(plan.Profile, plan.Overrides)
	if err == nil {
		m.mu.Lock()
		m.retries = retries
		m.mu.Unlock()
		if p.Interactive {
			err = process.ErrAuthRequired
		} else {
			err = m.Launch(m.ctx, p)
		}
	}
	if err == nil {
		err = m.FinishStart(m.ctx, p)
	}
	if err != nil {
		if errors.Is(err, process.ErrAuthRequired) {
			m.AbortStart("Auto-reconnect needs authentication; press start to reconnect.")
		}
		m.log.Warn("auto-reconnect failed", "err", err)
	}
}

func (m *Manager) endSessionLocked(reason string) {
	if m.session == nil {
		return
	}
	m.session.Ended = m.now()
	m.session.Reason = reason
	if m.history != nil {
		if err := m.history.Append(*m.session); err != nil {
			m.log.Warn("cannot write history", "err", err)
		}
	}
	m.session = nil
}

// ---------------------------------------------------------------------------
// Credentials

// SubmitCredentials answers a pending password request. When save is true
// and the request is for "Auth", the credentials are also written to the
// profile's credential file (mode 0600).
func (m *Manager) SubmitCredentials(ctx context.Context, username, password string, save bool) error {
	m.mu.Lock()
	c := m.client
	req := m.st.Password
	profilePath := m.st.ProfilePath
	m.mu.Unlock()
	if c == nil || req == nil {
		return ErrNoPrompt
	}
	if req.NeedUsername {
		if err := credentials.ValidateFields(username, password); err != nil {
			return err
		}
	}
	user := ""
	if req.NeedUsername {
		user = username
	}
	if err := c.SendCredentials(ctx, req.Type, user, password); err != nil {
		return err
	}
	m.mu.Lock()
	m.st.Password = nil
	m.st.Failure = nil
	m.setPhaseLocked(Connecting, "authenticating")
	m.mu.Unlock()
	m.changed()
	if save && req.NeedUsername && profilePath != "" {
		path := credentials.PathFor(profilePath, m.opts.CredExt)
		if err := credentials.Write(path, username, password); err != nil {
			return fmt.Errorf("connected, but saving credentials failed: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Stop

// StopTarget returns the process Stop would act on.
func (m *Manager) StopTarget() (process.Proc, Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st.Source == SourceManaged && m.st.PID > 0 {
		p, ok := m.procs.Inspect(m.st.PID)
		if !ok {
			p = process.Proc{PID: m.st.PID}
		}
		return p, SourceManaged, nil
	}
	if len(m.st.External) == 1 {
		p := m.st.External[0]
		if p.Unit != "" {
			return p, SourceSystemd, nil
		}
		return p, SourceExternal, nil
	}
	if len(m.st.External) > 1 {
		return process.Proc{}, SourceNone, fmt.Errorf("%d OpenVPN processes are running; stop them individually from the Network screen", len(m.st.External))
	}
	return process.Proc{}, SourceNone, ErrNotRunning
}

// Stop terminates the connection. Managed connections are stopped through
// the management socket; others are signalled (with elevation) or stopped
// via systemctl. It may return process.ErrAuthRequired.
func (m *Manager) Stop(ctx context.Context) error {
	target, src, err := m.StopTarget()
	if err != nil {
		return err
	}
	if src == SourceManaged && !m.procs.Alive(target.PID, target.StartTicks) {
		m.processGone(true)
		return nil
	}
	m.mu.Lock()
	c := m.client
	if src == SourceManaged {
		m.stopping = true
		m.setPhaseLocked(Disconnecting, "stopping")
	}
	m.mu.Unlock()
	m.changed()

	switch {
	case src == SourceManaged && c != nil:
		if err := c.Signal(ctx, "SIGTERM"); err != nil {
			m.log.Warn("management SIGTERM failed, signalling process", "err", err)
			if err := m.launch.Signal(ctx, target, "TERM"); err != nil {
				return m.stopFailed(err)
			}
		}
	case src == SourceSystemd:
		if err := process.RunSystemctl(ctx, "stop", target.Unit); err != nil {
			return err
		}
	default:
		if err := m.launch.Signal(ctx, target, "TERM"); err != nil {
			return m.stopFailed(err)
		}
	}
	return m.waitExit(ctx, target, src)
}

func (m *Manager) stopFailed(err error) error {
	m.mu.Lock()
	m.stopping = false
	if m.st.Phase == Disconnecting {
		m.setPhaseLocked(Unknown, "stop failed")
	}
	m.mu.Unlock()
	m.changed()
	return err
}

// WaitStopped waits for the process to exit after an interactive stop.
func (m *Manager) WaitStopped(ctx context.Context, p process.Proc, src Source) error {
	if src == SourceManaged {
		m.mu.Lock()
		m.stopping = true
		m.mu.Unlock()
	}
	return m.waitExit(ctx, p, src)
}

func (m *Manager) waitExit(ctx context.Context, p process.Proc, src Source) error {
	timeout := m.opts.StopTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for m.procs.Alive(p.PID, p.StartTicks) {
		if time.Now().After(deadline) {
			m.mu.Lock()
			m.st.Message = "OpenVPN did not exit after SIGTERM; you can force-kill it."
			m.mu.Unlock()
			m.changed()
			return ErrStopTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	if src == SourceManaged {
		m.mu.Lock()
		c := m.client
		m.mu.Unlock()
		if c != nil {
			// onDetach will finalise; wait briefly for it.
			select {
			case <-c.Done():
			case <-time.After(2 * time.Second):
			}
		}
		m.mu.Lock()
		stillManaged := m.st.Source == SourceManaged && m.st.Phase != Disconnected && m.st.Phase != Failed
		m.mu.Unlock()
		if stillManaged {
			m.processGone(true)
		}
	}
	_, _ = m.Refresh(ctx)
	return nil
}

// ForceKill sends SIGKILL to the stop target.
func (m *Manager) ForceKill(ctx context.Context) error {
	target, src, err := m.StopTarget()
	if err != nil {
		return err
	}
	if err := m.launch.Signal(ctx, target, "KILL"); err != nil {
		return err
	}
	return m.waitExit(ctx, target, src)
}

// InteractiveStopCmd returns a terminal command that stops the target with
// interactive authentication (sudo password / polkit prompt).
func (m *Manager) InteractiveStopCmd(p process.Proc, src Source) *exec.Cmd {
	if src == SourceSystemd && p.Unit != "" {
		return exec.Command("systemctl", "stop", p.Unit)
	}
	argv := process.KillCommand(p.PID, "TERM")
	switch m.launch.Effective() {
	case "sudo", "pkexec":
		w := m.launch.Wrap(argv)
		// Drop sudo's -n so it may prompt.
		out := w[:0:0]
		for _, a := range w {
			if a != "-n" {
				out = append(out, a)
			}
		}
		argv = out
	}
	return exec.Command(argv[0], argv[1:]...)
}

// ---------------------------------------------------------------------------
// Refresh

// Refresh re-reads the process table and interfaces and returns a snapshot.
func (m *Manager) Refresh(ctx context.Context) (Snapshot, error) {
	procs, perr := m.procs.FindOpenVPN()
	links, lerr := m.net.Links(ctx)

	m.mu.Lock()
	inst := m.inst
	managedPID := 0
	if m.st.Source == SourceManaged {
		managedPID = m.st.PID
	}
	sock := m.SocketPath()
	var external, servers []process.Proc
	var adopt *process.Proc
	for _, p := range procs {
		if p.PID == managedPID {
			continue
		}
		if p.Server {
			servers = append(servers, p)
			continue
		}
		if inst == nil && managedPID == 0 && p.Socket == sock {
			cp := p
			adopt = &cp // ours, but the instance record was lost
			continue
		}
		external = append(external, p)
	}
	m.st.External = external
	m.st.Servers = servers
	attached := m.client != nil
	phase := m.st.Phase
	m.mu.Unlock()

	if perr != nil {
		m.log.Debug("process scan failed", "err", perr)
	}

	// Managed process vanished without the management socket telling us.
	if managedPID > 0 && !attached && phase != Disconnected && phase != Failed {
		ticks := uint64(0)
		if inst != nil {
			ticks = inst.StartTicks
		}
		if !m.procs.Alive(managedPID, ticks) {
			m.mu.Lock()
			stopping := m.stopping
			m.mu.Unlock()
			m.processGone(stopping)
		} else if inst != nil {
			if err := m.attach(ctx, inst.Socket); err != nil {
				m.log.Debug("re-attach attempt failed", "err", err)
			}
		}
	}
	if adopt != nil {
		m.mu.Lock()
		m.st.Source = SourceManaged
		m.st.PID = adopt.PID
		m.st.Profile = filepath.Base(adopt.Config)
		m.st.ProfilePath = adopt.Config
		m.st.LogPath = adopt.Log
		m.inst = &process.Instance{Profile: m.st.Profile, ProfilePath: adopt.Config, PID: adopt.PID, StartTicks: adopt.StartTicks, Socket: sock, LogPath: adopt.Log, StartedAt: m.now()}
		m.session = &state.HistoryEntry{Profile: m.st.Profile, Started: m.now()}
		m.mu.Unlock()
		if err := m.attach(ctx, sock); err != nil {
			m.log.Debug("adopt attach failed", "err", err)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	snap := Snapshot{Links: links, LinksErr: lerr}
	m.resolveIfaceLocked(links)
	if m.st.Iface != "" {
		if l, ok := network.FindByName(links, m.st.Iface); ok {
			snap.VPNLink = &l
		}
	}
	m.deriveExternalLocked(links, &snap)
	snap.Status = m.copyStatusLocked()
	return snap, nil
}

func (m *Manager) resolveIfaceLocked(links []network.Link) {
	if m.st.Source != SourceManaged || len(links) == 0 {
		return
	}
	if m.st.Iface != "" {
		if _, ok := network.FindByName(links, m.st.Iface); ok {
			return
		}
	}
	// Fall back to the link carrying the tunnel IP reported by OpenVPN.
	for _, ipStr := range []string{m.st.LocalIP, m.st.LocalIPv6} {
		if ipStr == "" {
			continue
		}
		if addr, err := parseAddr(ipStr); err == nil {
			if l, ok := network.FindByAddr(links, addr); ok {
				m.st.Iface = l.Name
				return
			}
		}
	}
	if m.st.Phase == Connected && m.st.Iface != "" {
		m.st.Message = "Tunnel interface " + m.st.Iface + " no longer exists."
	}
}

// deriveExternalLocked fills the status from external processes when the
// the app does not manage a connection itself.
func (m *Manager) deriveExternalLocked(links []network.Link, snap *Snapshot) {
	if m.st.Source == SourceManaged && m.st.Phase != Disconnected && m.st.Phase != Failed {
		return
	}
	if len(m.st.External) == 0 {
		if m.st.Source == SourceExternal || m.st.Source == SourceSystemd {
			m.st.Source = SourceNone
			m.setPhaseLocked(Disconnected, "")
			m.st.Iface, m.st.PID, m.st.Unit, m.st.Profile = "", 0, "", ""
		}
		return
	}
	// Leave a failed managed session visible until a new action, but report
	// the external connection(s) in Status.External.
	if m.st.Source == SourceManaged && m.st.Phase == Failed {
		return
	}
	p := m.st.External[0]
	src := SourceExternal
	if p.Unit != "" {
		src = SourceSystemd
	}
	if m.st.Source != src || m.st.PID != p.PID {
		// Switching to a different connection: drop every field that
		// belonged to the previous one.
		m.st = Status{Phase: m.st.Phase, PhaseSince: m.st.PhaseSince, External: m.st.External, Servers: m.st.Servers}
	}
	m.st.Source = src
	m.st.PID = p.PID
	m.st.Unit = p.Unit
	m.st.Profile = p.Name()
	m.st.ProfilePath = p.Config
	m.st.LogPath = p.Log
	m.st.Attached = false
	cands := network.VPNLinks(links)
	var up []network.Link
	for _, l := range cands {
		if l.Up && (len(l.IPv4()) > 0 || len(l.IPv6()) > 0) {
			up = append(up, l)
		}
	}
	switch {
	case len(up) >= 1:
		m.st.Iface = up[0].Name
		if len(up) > 1 {
			m.st.Message = fmt.Sprintf("%d tunnel interfaces are up; showing %s.", len(up), up[0].Name)
		}
		if v4 := up[0].IPv4(); len(v4) > 0 {
			m.st.LocalIP = v4[0].Addr().String()
		}
		if v6 := up[0].IPv6(); len(v6) > 0 {
			m.st.LocalIPv6 = v6[0].Addr().String()
		}
		m.setPhaseLocked(Connected, "external process (not controlled by openvpn-wails)")
		l := up[0]
		snap.VPNLink = &l
	default:
		m.st.Iface = ""
		m.setPhaseLocked(Unknown, "external OpenVPN process without a tunnel interface")
	}
}

// ---------------------------------------------------------------------------
// Shutdown

// Close detaches from OpenVPN. If stopManaged is true, a managed connection
// is stopped first. It never stops external connections.
func (m *Manager) Close(ctx context.Context, stopManaged bool) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	managed := m.st.Source == SourceManaged && m.st.PID > 0
	m.mu.Unlock()
	if stopManaged && managed {
		if err := m.Stop(ctx); err != nil {
			m.log.Warn("stop on exit failed", "err", err)
		}
	}
	m.mu.Lock()
	m.closed = true
	c := m.client
	m.client = nil
	m.mu.Unlock()
	m.cancel()
	if c != nil {
		_ = c.Close()
	}
	done := make(chan struct{})
	go func() { m.bg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// Configure updates the settings that may change between connections:
// the OpenVPN binary (empty keeps the current one), the verbosity and the
// auto-reconnect policy.
func (m *Manager) Configure(binary string, verbosity int, autoReconnect bool, retryInterval time.Duration, maxRetries int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if binary != "" {
		m.opts.Binary = binary
	}
	m.opts.Verbosity = verbosity
	m.opts.AutoReconnect = autoReconnect
	m.opts.RetryInterval = retryInterval
	m.opts.MaxRetries = maxRetries
}
