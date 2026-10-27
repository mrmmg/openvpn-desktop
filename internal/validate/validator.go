package validate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mrmmg/openvpn-wails/internal/analyzer"
	"github.com/mrmmg/openvpn-wails/internal/credentials"
	"github.com/mrmmg/openvpn-wails/internal/ovpn"
	"github.com/mrmmg/openvpn-wails/internal/profile"
	"github.com/mrmmg/openvpn-wails/internal/state"
)

// RunResult is the outcome of running the openvpn binary.
type RunResult struct {
	Output   string
	ExitCode int
}

// OpenVPNRunner runs the openvpn binary in test mode. It is an interface so
// tests can avoid a real binary.
type OpenVPNRunner interface {
	// TestConfig runs `openvpn --log <tmp> ... --config <path> --test-crypto`
	// in dir with extra arguments appended and returns the captured output.
	TestConfig(ctx context.Context, dir, path string, extra []string) (RunResult, error)
}

// ExecRunner runs the real binary.
type ExecRunner struct {
	Binary string
	// TempDir receives the private log file of the test run.
	TempDir string
}

// ErrBinaryNotFound means the openvpn binary is missing.
var ErrBinaryNotFound = errors.New("openvpn binary not found")

// TestConfig implements OpenVPNRunner.
func (r ExecRunner) TestConfig(ctx context.Context, dir, path string, extra []string) (RunResult, error) {
	if _, err := os.Stat(r.Binary); err != nil {
		return RunResult{}, fmt.Errorf("%w at %s", ErrBinaryNotFound, r.Binary)
	}
	if err := os.MkdirAll(r.TempDir, 0o700); err != nil {
		return RunResult{}, err
	}
	tmp, err := os.MkdirTemp(r.TempDir, "validate-")
	if err != nil {
		return RunResult{}, err
	}
	defer os.RemoveAll(tmp)
	logPath := filepath.Join(tmp, "test.log")
	// --log first: OpenVPN applies the first --log/log while parsing, so a
	// `log` line inside the profile cannot capture or truncate anything.
	// --cipher before --config: only used when the profile sets none.
	args := []string{"--log", logPath, "--cipher", "AES-256-GCM", "--cd", dir, "--config", path}
	args = append(args, extra...)
	args = append(args, "--test-crypto", "--verb", "3")
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()
	logData, _ := os.ReadFile(logPath)
	res := RunResult{Output: string(logData) + out.String()}
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		res.ExitCode = ee.ExitCode()
	case ctx.Err() != nil:
		return res, fmt.Errorf("openvpn test timed out: %w", ctx.Err())
	default:
		return res, runErr
	}
	return res, nil
}

// Validator runs all validation layers.
type Validator struct {
	Runner  OpenVPNRunner
	CredExt string
	// ExpiryWarning flags certificates expiring within this window.
	ExpiryWarning time.Duration
	Now           func() time.Time
	// Timeout bounds the openvpn invocation.
	Timeout time.Duration
}

// Validate checks a profile. Launch overrides are included so the
// combination that will actually run is validated.
func (v *Validator) Validate(ctx context.Context, p profile.Profile, ov state.Overrides) Report {
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	r := Report{Profile: p.Name, At: now()}
	cfg := v.static(&r, p)
	if cfg == nil {
		r.add(LayerParser, Skipped, "not run", "static checks failed to read the file", 0)
		r.add(LayerCrypto, Skipped, "not run", "", 0)
		r.add(LayerCredentials, Skipped, "not run", "", 0)
		return r
	}
	extra := analyzer.OverrideArgs(ov)
	if plan := credentials.MakePlan(cfg, p.Path, v.CredExt); plan.Source == credentials.SavedFile {
		extra = append(extra, "--auth-user-pass", plan.Path)
	}
	v.parser(ctx, &r, p, extra)
	v.crypto(&r, cfg, p)
	v.credentials(&r, cfg, p)
	return r
}

// static performs checks that need nothing but the file system.
func (v *Validator) static(r *Report, p profile.Profile) *ovpn.Config {
	const L = LayerStatic
	fi, err := os.Stat(p.Path)
	if err != nil {
		r.add(L, Error, "profile file not accessible", err.Error(), 0)
		return nil
	}
	if fi.Size() == 0 {
		r.add(L, Error, "profile file is empty", "", 0)
		return nil
	}
	if fi.Size() > ovpn.MaxFileSize {
		r.add(L, Error, "profile file is too large", fmt.Sprintf("%d bytes", fi.Size()), 0)
		return nil
	}
	cfg, err := ovpn.ParseFile(p.Path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			r.add(L, Error, "profile file is not readable", "permission denied for "+p.Path, 0)
		} else {
			r.add(L, Error, "cannot read profile", err.Error(), 0)
		}
		return nil
	}
	r.add(L, OK, "file readable", fmt.Sprintf("%s, %d directives", p.Path, len(cfg.Directives)), 0)
	for _, pe := range cfg.Errors {
		r.add(L, Error, "syntax error", pe.Msg, pe.Line)
	}
	if !cfg.IsClient() {
		r.add(L, Warn, "not a client profile", "no 'client' (or 'tls-client' + 'pull') directive", 0)
	}
	remotes := cfg.Remotes()
	if len(remotes) == 0 {
		r.add(L, Error, "no server", "the profile has no 'remote' directive", 0)
	} else {
		var hosts []string
		for _, rm := range remotes {
			hosts = append(hosts, fmt.Sprintf("%s:%d/%s", rm.Host, rm.Port, rm.Proto))
		}
		r.add(L, OK, plural(len(remotes), "remote"), strings.Join(hosts, ", "), 0)
	}
	if _, typ := cfg.Device(); typ == "null" {
		r.add(L, Warn, "null device", "dev null creates no tunnel interface", 0)
	}
	for _, ref := range cfg.FileRefs(p.Dir) {
		if ref.Inline {
			continue
		}
		if ref.Directive == "auth-user-pass" {
			continue // handled by the credentials layer
		}
		f, err := os.Open(ref.Resolved)
		switch {
		case err == nil:
			_ = f.Close()
			r.add(L, OK, ref.Directive+" file found", ref.Path, ref.Line)
		case errors.Is(err, fs.ErrNotExist):
			r.add(L, Error, ref.Directive+" file missing", ref.Resolved, ref.Line)
		case errors.Is(err, fs.ErrPermission):
			r.add(L, Warn, ref.Directive+" file not readable by you", ref.Resolved+" (OpenVPN runs as root and may still read it)", ref.Line)
		default:
			r.add(L, Error, ref.Directive+" file not accessible", err.Error(), ref.Line)
		}
	}
	for _, name := range []string{"comp-lzo", "compress"} {
		if d, ok := cfg.Last(name); ok {
			r.add(L, Warn, "compression enabled", "'"+d.String()+"' is deprecated and enables VORACLE-style attacks", d.Line)
		}
	}
	if !cfg.Has("remote-cert-tls") && !cfg.Has("verify-x509-name") && !cfg.Has("ns-cert-type") && !cfg.Has("secret") {
		r.add(L, Warn, "server identity not pinned", "add 'remote-cert-tls server' to prevent man-in-the-middle attacks by other clients of the same CA", 0)
	}
	if (cfg.Has("user") || cfg.Has("group")) && !(cfg.Has("persist-tun") && cfg.Has("persist-key")) {
		r.add(L, Warn, "privilege drop without persist-tun/persist-key", "OpenVPN may fail to reconnect after dropping root", 0)
	}
	for _, name := range []string{"daemon", "log", "log-append", "syslog", "management", "writepid"} {
		if d, ok := cfg.Last(name); ok {
			r.add(L, Info, "'"+name+"' is overridden", "openvpn-wails manages logging, daemon mode and the management socket itself", d.Line)
		}
	}
	scripts := cfg.Scripts()
	if len(scripts) > 0 {
		var cmds []string
		for _, s := range scripts {
			cmds = append(cmds, s.Directive+" "+s.Command)
		}
		sev, detail := Info, "runs as root: "+strings.Join(cmds, "; ")
		if cfg.ScriptSecurity() < 2 {
			sev, detail = Warn, "script-security < 2, these will NOT run: "+strings.Join(cmds, "; ")
		}
		r.add(L, sev, plural(len(scripts), "script"), detail, scripts[0].Line)
	}
	st := analyzer.AnalyzeStatic(cfg)
	r.add(L, Info, "routing (requested)", fmt.Sprintf("IPv4 %s, IPv6 %s", st.IPv4.Mode, st.IPv6.Mode), 0)
	return cfg
}

var (
	tsPrefix  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)? `)
	optErrRe  = regexp.MustCompile(`Options error: (.*)`)
	warningRe = regexp.MustCompile(`(?i)^WARNING: (.*)`)
)

func (v *Validator) parser(ctx context.Context, r *Report, p profile.Profile, extra []string) {
	const L = LayerParser
	if v.Runner == nil {
		r.add(L, Skipped, "openvpn check not available", "", 0)
		return
	}
	timeout := v.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := v.Runner.TestConfig(ctx, p.Dir, p.Path, extra)
	if err != nil {
		if errors.Is(err, ErrBinaryNotFound) {
			r.add(L, Error, "openvpn binary not found", err.Error(), 0)
		} else {
			r.add(L, Error, "openvpn check could not run", err.Error(), 0)
		}
		return
	}
	var errs, warns []string
	succeeded := false
	for _, line := range strings.Split(res.Output, "\n") {
		line = strings.TrimSpace(tsPrefix.ReplaceAllString(strings.TrimSpace(line), ""))
		switch {
		case line == "":
		case strings.Contains(line, "Options error: Please correct these errors"):
		case optErrRe.MatchString(line):
			errs = append(errs, optErrRe.FindStringSubmatch(line)[1])
		case warningRe.MatchString(line):
			warns = append(warns, warningRe.FindStringSubmatch(line)[1])
		case strings.Contains(line, "self-test mode SUCCEEDED"):
			succeeded = true
		}
	}
	for _, e := range errs {
		r.add(L, Error, "openvpn rejected the configuration", e, lineFromOptionsError(e))
	}
	for _, w := range warns {
		r.add(L, Warn, "openvpn warning", w, 0)
	}
	switch {
	case len(errs) > 0:
	case res.ExitCode != 0:
		r.add(L, Error, fmt.Sprintf("openvpn exited with status %d", res.ExitCode), trimLines(res.Output, 6), 0)
	case succeeded:
		r.add(L, OK, "configuration accepted by openvpn", "parsed with --test-crypto (no connection made)", 0)
	default:
		r.add(L, Warn, "openvpn check inconclusive", trimLines(res.Output, 6), 0)
	}
}

var optLineRe = regexp.MustCompile(`:(\d+): `)

func lineFromOptionsError(s string) int {
	if m := optLineRe.FindStringSubmatch(s); m != nil {
		var n int
		_, _ = fmt.Sscanf(m[1], "%d", &n)
		return n
	}
	return 0
}

func (v *Validator) credentials(r *Report, cfg *ovpn.Config, p profile.Profile) {
	const L = LayerCredentials
	plan := credentials.MakePlan(cfg, p.Path, v.CredExt)
	savedPath := credentials.PathFor(p.Path, v.CredExt)
	switch plan.Source {
	case credentials.NotRequired:
		r.add(L, OK, "not required", "profile has no auth-user-pass", 0)
		if st := credentials.Inspect(savedPath); st.Exists {
			r.add(L, Info, "unused credential file", filepath.Base(savedPath)+" exists but the profile does not use auth-user-pass", 0)
		}
		return
	case credentials.Inline:
		r.add(L, OK, "inline credentials", "embedded <auth-user-pass> block", 0)
		return
	case credentials.Prompt:
		r.add(L, Info, "credentials missing", "no "+filepath.Base(savedPath)+"; you will be asked when connecting", 0)
		return
	}
	st := plan.Status
	name := filepath.Base(plan.Path)
	if !st.Exists {
		r.add(L, Error, "credential file missing", plan.Path+" (named by auth-user-pass in the profile)", cfg.Auth().Line)
		return
	}
	if st.Err != nil {
		r.add(L, Error, "credential file unreadable", st.Err.Error(), 0)
		return
	}
	switch {
	case !st.HasUsername:
		r.add(L, Error, "credential file has no username", name, 0)
	case !st.HasPassword:
		r.add(L, Info, "username only", name+": the password will be asked when connecting", 0)
	default:
		r.add(L, OK, "credentials available", name+" (user "+st.Username+")", 0)
	}
	if st.Insecure() {
		r.add(L, Warn, "credential file permissions too open", fmt.Sprintf("%s is %04o; should be 0600", name, st.Mode.Perm()), 0)
	}
	if !st.OwnedByUser && os.Geteuid() != 0 {
		r.add(L, Warn, "credential file owned by another user", name, 0)
	}
}
