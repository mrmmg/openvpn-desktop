package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/analyzer"
	"github.com/mrmmg/openvpn-desktop/internal/appconfig"
	"github.com/mrmmg/openvpn-desktop/internal/ipcheck"
	"github.com/mrmmg/openvpn-desktop/internal/network"
	"github.com/mrmmg/openvpn-desktop/internal/ovpn"
	"github.com/mrmmg/openvpn-desktop/internal/process"
	"github.com/mrmmg/openvpn-desktop/internal/state"
	"github.com/mrmmg/openvpn-desktop/internal/validate"
	"github.com/mrmmg/openvpn-desktop/internal/vpn"
)

const (
	trafficHistory  = 60
	maxRoutesShown  = 200
	refreshInterval = 2 * time.Second
)

// Controller drives the connection: it starts/stops OpenVPN through the
// manager, measures traffic and publishes state to the frontend.
type Controller struct {
	core *Core

	// opMu serialises connect/disconnect operations.
	opMu     sync.Mutex
	starting bool

	mu      sync.Mutex
	meter   *network.Meter
	meterID string
	history []TrafficSample
	det     details
	detKey  string
	public  publicIP
	lastMsg []byte
	// lastState is the previous connection state, for notifications;
	// dropped is true after an established connection was lost and until it
	// is back or given up.
	lastState string
	dropped   bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// details are facts derived from the session log and the kernel.
type details struct {
	Protocol, Cipher, Auth string
	DNS, Routes            []string
}

type publicIP struct {
	key   string
	state string
	ip    string
}

// NewController starts the background loops.
func NewController(core *Core) *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{core: core, meter: network.NewMeter(trafficHistory), ctx: ctx, cancel: cancel}
	c.wg.Add(1)
	go c.run()
	return c
}

// Shutdown stops the loops.
func (c *Controller) Shutdown() {
	c.cancel()
	c.wg.Wait()
}

// ---------------------------------------------------------------------------
// Actions

// Connect starts the profile. It returns as soon as the launch is under way;
// progress and failures arrive through state events.
func (c *Controller) Connect(id string, acknowledgeScripts, askCredentials bool) Result {
	if !c.opMu.TryLock() {
		return fail(CodeBusy, "Another connection operation is still in progress.")
	}
	locked := true
	defer func() {
		if locked {
			c.opMu.Unlock()
		}
	}()

	st := c.core.VPN.Status()
	if st.Active() || st.Phase == vpn.Unknown && st.Source == vpn.SourceManaged {
		return fail(CodeBusy, c.busyMessage(st))
	}
	if ext := c.externalClients(st); len(ext) > 0 {
		return fail(CodeExternal, "Another OpenVPN process is already running ("+strings.Join(ext, ", ")+"). Stop it first; only one VPN connection can be active.")
	}
	entry, err := c.core.Lib.Get(id)
	if err != nil {
		return fail(CodeNotFound, "The profile no longer exists.")
	}
	conf := c.core.Cfg.Get()
	bin, err := process.LocateBinary(conf.OpenVPNBinary)
	if err != nil {
		return fail(CodeBinaryMissing, err.Error())
	}
	cfg, err := ovpn.ParseFile(entry.Profile.Path)
	if err != nil {
		return fail(CodeInvalid, "Cannot read the profile: "+err.Error())
	}
	if risks := scriptRisks(cfg); len(risks) > 0 && !acknowledgeScripts {
		return fail(CodeScripts, "This profile asks OpenVPN to run external commands as administrator:\n"+strings.Join(risks, "\n"))
	}

	c.starting = true
	c.publish()
	rep := (&validate.Validator{
		Runner:  validate.ExecRunner{Binary: bin, TempDir: c.core.Dirs.Runtime},
		CredExt: credExt,
	}).Validate(c.ctx, entry.Profile, state.Overrides{})
	if !rep.Valid() {
		c.starting = false
		c.publish()
		return fail(CodeInvalid, rep.FirstError())
	}
	c.core.VPN.Configure(bin, conf.LogVerbosity, conf.AutoReconnect, time.Duration(conf.ReconnectInterval)*time.Second, conf.ReconnectAttempts)
	plan, err := c.core.VPN.PrepareStartWith(entry.Profile, state.Overrides{}, askCredentials)
	if err != nil {
		c.starting = false
		c.publish()
		return failErr(CodeError, err)
	}
	locked = false // the goroutine releases the lock
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.opMu.Unlock()
		c.launch(plan, entry.Name)
	}()
	return ok()
}

func (c *Controller) launch(plan *vpn.StartPlan, name string) {
	defer func() {
		c.starting = false
		c.publish()
	}()
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Minute)
	defer cancel()
	if err := c.core.VPN.Launch(ctx, plan); err != nil {
		c.core.Log.Warn("launch failed", "profile", plan.Profile.Name, "err", err)
		if errors.Is(err, process.ErrCancelled) {
			c.notify("info", "Connection cancelled", "Administrator authorization is required to start OpenVPN.")
		}
		return
	}
	if err := c.core.VPN.FinishStart(ctx, plan); err != nil {
		c.core.Log.Warn("start failed", "profile", plan.Profile.Name, "err", err)
	}
}

// Disconnect requests a clean shutdown of the active connection.
func (c *Controller) Disconnect() Result {
	st := c.core.VPN.Status()
	if st.Phase == vpn.Disconnected || st.Phase == vpn.Failed {
		return ok()
	}
	if st.Source != vpn.SourceManaged {
		return fail(CodeExternal, "This connection was not started by the app and cannot be controlled from here.")
	}
	if !c.opMu.TryLock() {
		return fail(CodeBusy, "OpenVPN is still starting; wait a moment and try again.")
	}
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer c.opMu.Unlock()
		ctx, cancel := context.WithTimeout(c.ctx, 45*time.Second)
		defer cancel()
		if err := c.core.VPN.Stop(ctx); err != nil {
			c.core.Log.Warn("stop failed", "err", err)
			msg := err.Error()
			if errors.Is(err, vpn.ErrStopTimeout) {
				msg = "OpenVPN did not stop in time. You can force it to stop (this asks for administrator authorization)."
			}
			c.notify("error", "Could not disconnect", msg)
		}
		c.publish()
	}()
	return ok()
}

// ForceDisconnect kills OpenVPN (SIGKILL) after a failed clean stop.
func (c *Controller) ForceDisconnect() Result {
	if !c.opMu.TryLock() {
		return fail(CodeBusy, "Another connection operation is in progress.")
	}
	defer c.opMu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
	defer cancel()
	if err := c.core.VPN.ForceKill(ctx); err != nil {
		if errors.Is(err, process.ErrCancelled) {
			return fail(CodeCancelled, "Authorization was cancelled.")
		}
		return failErr(CodeError, err)
	}
	return ok()
}

// SubmitCredentials answers OpenVPN's credential prompt. The values are
// forwarded to the management interface and never stored or logged.
func (c *Controller) SubmitCredentials(username, password string) Result {
	ctx, cancel := context.WithTimeout(c.ctx, 15*time.Second)
	defer cancel()
	if err := c.core.VPN.SubmitCredentials(ctx, username, password, false); err != nil {
		return failErr(CodeError, err)
	}
	return ok()
}

// CancelCredentials aborts the connection attempt waiting for credentials.
func (c *Controller) CancelCredentials() Result { return c.Disconnect() }

func (c *Controller) busyMessage(st vpn.Status) string {
	name := st.Profile
	if id := c.core.Lib.IDForPath(st.ProfilePath); id != "" {
		if e, err := c.core.Lib.Get(id); err == nil {
			name = e.Name
		}
	}
	if name == "" {
		return "A VPN connection is already active. Disconnect it first."
	}
	return fmt.Sprintf("%q is %s. Disconnect it first; only one VPN connection can be active at a time.", name, strings.ToLower(stateName(st.Phase)))
}

func (c *Controller) externalClients(st vpn.Status) []string {
	var out []string
	for _, p := range st.External {
		out = append(out, "PID "+strconv.Itoa(p.PID))
	}
	return out
}

// scriptRisks lists configured scripts that OpenVPN would run as root.
func scriptRisks(cfg *ovpn.Config) []string {
	if cfg.ScriptSecurity() < 2 {
		return nil
	}
	var out []string
	for _, s := range cfg.Scripts() {
		out = append(out, fmt.Sprintf("  %s %s", s.Directive, s.Command))
	}
	return out
}

// ---------------------------------------------------------------------------
// Event loop

func (c *Controller) run() {
	defer c.wg.Done()
	refresh := time.NewTicker(refreshInterval)
	traffic := time.NewTicker(time.Second)
	defer refresh.Stop()
	defer traffic.Stop()
	c.refresh()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.core.VPN.Updates():
			c.publish()
		case n := <-c.core.VPN.Notices():
			c.handleNotice(n)
		case <-refresh.C:
			c.refresh()
		case <-traffic.C:
			c.sample()
		}
	}
}

func (c *Controller) refresh() {
	ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	_, _ = c.core.VPN.Refresh(ctx)
	c.updateDetails(ctx)
	c.publish()
}

func (c *Controller) handleNotice(n vpn.Notice) {
	switch n.Kind {
	case vpn.NoticeConnected:
		c.notify("success", "Connected", c.profileLabel(c.core.VPN.Status()))
	case vpn.NoticeDisconnected:
		c.notify("info", "Disconnected", n.Message)
	case vpn.NoticeDropped:
		c.notify("warning", "Connection lost", n.Message)
	case vpn.NoticeAuthFailed:
		c.notify("error", "Authentication failed", "Check your username and password.")
	case vpn.NoticeError:
		c.notify("error", n.Title, n.Message)
	case vpn.NoticeReconnecting:
		c.notify("warning", "Reconnecting", n.Message)
	}
}

func (c *Controller) profileLabel(st vpn.Status) string {
	if id := c.core.Lib.IDForPath(st.ProfilePath); id != "" {
		if e, err := c.core.Lib.Get(id); err == nil {
			return e.Name
		}
	}
	return st.Profile
}

func (c *Controller) notify(level, title, msg string) {
	c.core.emit(EventNotice, Notice{Level: level, Title: title, Message: msg})
}

// State returns the current state (used for the initial load).
func (c *Controller) State() ConnectionState { return c.build() }

// Traffic returns the recent samples (used for the initial load).
func (c *Controller) Traffic() []TrafficSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]TrafficSample(nil), c.history...)
}

func (c *Controller) publish() {
	s := c.build()
	data, _ := json.Marshal(s)
	c.mu.Lock()
	same := string(data) == string(c.lastMsg)
	c.lastMsg = data
	c.mu.Unlock()
	if !same {
		c.core.emit(EventState, s)
	}
	c.notifyTransition(s)
}

// notifyTransition shows a desktop notification when the tunnel comes up,
// goes down, is lost, or is being re-established. The first state seen at
// start-up never notifies.
func (c *Controller) notifyTransition(s ConnectionState) {
	c.mu.Lock()
	prev := c.lastState
	c.lastState = s.State
	dropped := c.dropped
	c.mu.Unlock()
	if prev == "" || prev == s.State || c.core.Notify == nil {
		return
	}
	setDropped := func(v bool) {
		c.mu.Lock()
		c.dropped = v
		c.mu.Unlock()
	}
	name := s.ActiveID
	if e, err := c.core.Lib.Get(s.ActiveID); err == nil {
		name = e.Name
	}
	switch {
	case s.State == "connected":
		setDropped(false)
		title := "Connected"
		if dropped {
			title = "Reconnected"
		}
		body := "VPN IP " + s.VPNIP
		if s.VPNIP == "" {
			body = "The tunnel is up"
		}
		if s.Interface != "" {
			body += " · " + s.Interface
		}
		c.core.Notify(title, name, body)
	case prev == "connected" && s.State == "connecting":
		setDropped(true)
		c.core.Notify("Connection lost", name, "Reconnecting…")
	case prev == "connected" && s.State == "error":
		setDropped(true)
		body := s.Error
		if s.Message != "" {
			body = strings.TrimSpace(body + "\n" + s.Message)
		}
		c.core.Notify("Disconnected unexpectedly", name, body)
	case dropped && prev == "error" && s.State == "connecting":
		c.core.Notify("Reconnecting", name, "Starting OpenVPN again…")
	case dropped && prev == "connecting" && s.State == "error":
		setDropped(false)
		c.core.Notify("Reconnect failed", name, s.Error)
	case s.State == "disconnected" && (prev == "connected" || prev == "disconnecting"):
		setDropped(false)
		c.core.Notify("Disconnected", name, "The VPN connection was closed.")
	case s.State == "disconnected" && dropped:
		setDropped(false)
	case prev == "connecting" && s.State == "error":
		c.core.Notify("Connection failed", name, s.Error)
	}
}

// ---------------------------------------------------------------------------
// State

func stateName(p vpn.Phase) string {
	switch p {
	case vpn.Connected:
		return "connected"
	case vpn.Connecting:
		return "connecting"
	case vpn.Disconnecting:
		return "disconnecting"
	case vpn.Failed:
		return "error"
	case vpn.Unknown:
		return "unknown"
	}
	return "disconnected"
}

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func (c *Controller) build() ConnectionState {
	st := c.core.VPN.Status()
	s := ConnectionState{
		State:          stateName(st.Phase),
		Detail:         st.Detail,
		ConnectedSince: ms(st.ConnectedSince),
		PhaseSince:     ms(st.PhaseSince),
		Interface:      st.Iface,
		VPNIP:          st.LocalIP,
		ServerIP:       st.RemoteIP,
		BytesIn:        st.BytesIn,
		BytesOut:       st.BytesOut,
		Message:        st.Message,
		Reconnects:     st.Reconnects,
		PublicIPState:  "off",
	}
	c.mu.Lock()
	s.Starting = c.starting
	c.mu.Unlock()
	if st.Phase != vpn.Connected {
		s.ConnectedSince = 0
	}
	if st.Source == vpn.SourceManaged || st.Active() {
		s.ActiveID = c.core.Lib.IDForPath(st.ProfilePath)
	}
	if s.State == "disconnected" && !s.Starting && st.Source != vpn.SourceManaged {
		s.ActiveID = ""
	}
	if st.RemotePort != "" {
		s.ServerPort = st.RemotePort
	}
	if st.ProfilePath != "" {
		if cfg, err := ovpn.ParseFile(st.ProfilePath); err == nil {
			if r := cfg.Remotes(); len(r) > 0 {
				s.ServerHost = r[0].Host
				s.ServerPort = strconv.Itoa(r[0].Port)
				s.Protocol = strings.ToUpper(r[0].Proto)
			}
		}
	}
	if st.Failure != nil && (st.Phase == vpn.Failed || st.Phase == vpn.Disconnected || st.Password != nil) {
		s.Error, s.ErrorHint, s.AuthFailed = st.Failure.Summary, st.Failure.Hint, st.Failure.Auth
	}
	if st.Password != nil && !st.Password.Failed {
		s.Credentials = &CredentialRequest{Type: st.Password.Type, NeedUsername: st.Password.NeedUsername, Challenge: st.Password.Challenge}
	}
	for _, p := range st.External {
		s.External = append(s.External, fmt.Sprintf("PID %d", p.PID))
	}
	if st.Phase == vpn.Connected {
		c.mu.Lock()
		d, pub := c.det, c.public
		c.mu.Unlock()
		if d.Protocol != "" {
			s.Protocol = d.Protocol
		}
		s.Cipher, s.Auth, s.DNS, s.Routes = d.Cipher, d.Auth, d.DNS, d.Routes
		if s.Auth == "" && isAEAD(s.Cipher) {
			s.Auth = "AEAD (integrated in cipher)"
		}
		if pub.state != "" {
			s.PublicIPState, s.PublicIP = pub.state, pub.ip
		}
	}
	return s
}

var (
	cipherRe = regexp.MustCompile(`(?i)data channel[^']*cipher '([^']+)'`)
	hashRe   = regexp.MustCompile(`(?i)message hash '([^']+)'`)
	protoRe  = regexp.MustCompile(`(UDP|TCP)v?[46]?(?:_CLIENT)? link remote`)
	dnsRe    = regexp.MustCompile(`^(?:dhcp-option DNS6? |dns server \d+ address )(.+)$`)
)

// scanLog derives protocol, cipher and auth from OpenVPN's own log.
// isAEAD reports ciphers that authenticate data themselves, so OpenVPN
// logs no separate HMAC digest.
func isAEAD(cipher string) bool {
	c := strings.ToUpper(cipher)
	return strings.Contains(c, "-GCM") || strings.Contains(c, "POLY1305")
}

func scanLog(lines []string) (proto, cipher, auth string) {
	for _, l := range lines {
		if m := protoRe.FindStringSubmatch(l); m != nil {
			proto = m[1]
		}
		if m := cipherRe.FindStringSubmatch(l); m != nil {
			cipher = m[1]
		}
		if m := hashRe.FindStringSubmatch(l); m != nil {
			auth = m[1]
		}
	}
	return
}

// pushedDNS lists DNS servers the server pushed and the config accepted.
func pushedDNS(p analyzer.Pushed) []string {
	var out []string
	for _, o := range p.Options {
		if !o.Applied {
			continue
		}
		if m := dnsRe.FindStringSubmatch(o.Text); m != nil {
			for _, a := range strings.Split(m[1], ",") {
				a = strings.TrimSpace(a)
				if _, err := netip.ParseAddr(a); err == nil && !contains(out, a) {
					out = append(out, a)
				}
			}
		}
	}
	return out
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// updateDetails refreshes the cached log/kernel-derived facts while
// connected and starts the public-IP lookup once per session.
func (c *Controller) updateDetails(ctx context.Context) {
	st := c.core.VPN.Status()
	if st.Phase != vpn.Connected {
		c.mu.Lock()
		c.det, c.detKey = details{}, ""
		c.mu.Unlock()
		return
	}
	key := st.ProfilePath + "|" + st.ConnectedSince.String()
	lines := c.core.VPN.SessionLog()
	var d details
	d.Protocol, d.Cipher, d.Auth = scanLog(lines)
	if cfg, err := ovpn.ParseFile(st.ProfilePath); err == nil {
		d.DNS = pushedDNS(analyzer.ParsePushed(lines, analyzer.AnalyzeStatic(cfg)))
	}
	if st.Iface != "" {
		if routes, err := c.core.Net.Routes(ctx); err == nil {
			d.Routes = tunnelRoutes(routes, st.Iface)
		}
	}
	c.mu.Lock()
	c.det, c.detKey = d, key
	conf := c.core.Cfg.Get()
	startLookup := c.public.key != key && conf.PublicIPCheck && len(enabledServices(conf.IPServices)) > 0 && st.Iface != ""
	if startLookup {
		c.public = publicIP{key: key, state: "checking"}
	} else if c.public.key != key {
		c.public = publicIP{key: key, state: "off"}
	}
	c.mu.Unlock()
	if startLookup {
		c.wg.Add(1)
		go c.lookupPublicIP(key)
	}
}

func enabledServices(in []appconfig.IPService) []ipcheck.IPService {
	var out []ipcheck.IPService
	for _, s := range in {
		if s.Enabled {
			out = append(out, ipcheck.IPService{Name: s.Name, URL: s.URL, Enabled: true})
		}
	}
	return out
}

// routeLabel is a compact, readable form of a route.
func routeLabel(r network.Route) string {
	dst := r.Dst.String()
	if r.IsDefault() {
		dst = "default"
	}
	if r.Gateway.IsValid() {
		return dst + " via " + r.Gateway.String()
	}
	return dst + " (direct)"
}

func tunnelRoutes(routes []network.Route, iface string) []string {
	var out []string
	more := 0
	for _, r := range routes {
		if r.Dev != iface || !r.Dst.IsValid() || r.Dst.Addr().IsMulticast() || r.Dst.Addr().IsLinkLocalUnicast() {
			continue
		}
		if len(out) >= maxRoutesShown {
			more++
			continue
		}
		out = append(out, routeLabel(r))
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("+%d more", more))
	}
	return out
}

func (c *Controller) lookupPublicIP(key string) {
	defer c.wg.Done()
	ctx, cancel := context.WithTimeout(c.ctx, 12*time.Second)
	defer cancel()
	// Give routes and DNS a moment to settle after "connected".
	select {
	case <-time.After(1500 * time.Millisecond):
	case <-ctx.Done():
		return
	}
	res := c.core.IP.Check(ctx, enabledServices(c.core.Cfg.Get().IPServices))
	sum := ipcheck.Summarize(res)
	next := publicIP{key: key, state: "failed"}
	if ip, found := sum.Primary(); found && sum.Consistent() {
		next = publicIP{key: key, state: "ok", ip: ip.String()}
	}
	c.mu.Lock()
	if c.public.key == key {
		c.public = next
	}
	c.mu.Unlock()
	c.publish()
}

// sample reads the byte counters once and emits the derived rate.
func (c *Controller) sample() {
	st := c.core.VPN.Status()
	if st.Phase != vpn.Connected {
		c.mu.Lock()
		if len(c.history) > 0 || c.meterID != "" {
			c.history, c.meterID = nil, ""
			c.meter.Reset()
		}
		c.mu.Unlock()
		return
	}
	var cnt network.Counters
	source := "mgmt"
	if st.Iface != "" {
		if v, err := c.core.Net.Counters(st.Iface); err == nil {
			cnt, source = v, "iface:"+st.Iface
		}
	}
	if source == "mgmt" {
		cnt = network.Counters{RxBytes: st.BytesIn, TxBytes: st.BytesOut}
	}
	id := st.ProfilePath + "|" + st.ConnectedSince.String() + "|" + source
	now := time.Now()
	c.mu.Lock()
	if id != c.meterID {
		// New session or measurement source: counters are not comparable.
		c.meter.Reset()
		c.history = nil
		c.meterID = id
	}
	r, okRate := c.meter.Add(cnt, now)
	if !okRate {
		c.mu.Unlock()
		return
	}
	s := TrafficSample{Time: now.UnixMilli(), Down: r.Down, Up: r.Up, TotalDown: c.meter.TotalDown, TotalUp: c.meter.TotalUp}
	c.history = append(c.history, s)
	if len(c.history) > trafficHistory {
		c.history = c.history[len(c.history)-trafficHistory:]
	}
	c.mu.Unlock()
	c.core.emit(EventTraffic, s)
}

// ---------------------------------------------------------------------------

// LastConnected returns when each profile was last connected (by profile
// file name), from the session history.
func (c *Controller) lastConnected() map[string]int64 {
	out := map[string]int64{}
	list, err := c.core.Hist.List()
	if err != nil {
		return out
	}
	for _, e := range list {
		if t := ms(e.Connected); t > out[e.Profile] {
			out[e.Profile] = t
		}
	}
	return out
}
