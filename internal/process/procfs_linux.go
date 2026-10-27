//go:build linux

package process

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultTable returns the process table of the running platform.
func DefaultTable() ProcFS { return DefaultProcFS }

// ProcFS reads processes from /proc (Linux).
type ProcFS struct {
	Root string // normally /proc
}

// DefaultProcFS reads the real /proc.
var DefaultProcFS = ProcFS{Root: "/proc"}

// FindOpenVPN lists running openvpn processes.
func (fs ProcFS) FindOpenVPN() ([]Proc, error) {
	entries, err := os.ReadDir(fs.Root)
	if err != nil {
		return nil, err
	}
	var out []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		p, ok := fs.Inspect(pid)
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// Inspect returns information about pid if it is an openvpn process.
func (fs ProcFS) Inspect(pid int) (Proc, bool) {
	dir := filepath.Join(fs.Root, strconv.Itoa(pid))
	comm, err := os.ReadFile(filepath.Join(dir, "comm"))
	if err != nil {
		return Proc{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil || len(raw) == 0 {
		return Proc{}, false
	}
	args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	if strings.TrimSpace(string(comm)) != "openvpn" && filepath.Base(args[0]) != "openvpn" {
		return Proc{}, false
	}
	p := Proc{PID: pid, Args: args, UID: -1}
	if st, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
		p.StartTicks = parseStartTicks(st)
	}
	if status, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
		p.UID = parseUID(status)
	}
	parseArgs(&p)
	if cg, err := os.ReadFile(filepath.Join(dir, "cgroup")); err == nil {
		p.Unit = parseUnit(cg)
	}
	p.Server = isServer(p)
	return p, true
}

// Alive reports whether pid still is the same openvpn process (guards
// against PID reuse by comparing the start time when known).
func (fs ProcFS) Alive(pid int, startTicks uint64) bool {
	p, ok := fs.Inspect(pid)
	if !ok {
		return false
	}
	return startTicks == 0 || p.StartTicks == startTicks
}

// parseStartTicks returns field 22 of /proc/PID/stat. The comm field may
// contain spaces and parentheses, so parsing starts after the last ')'.
func parseStartTicks(stat []byte) uint64 {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0
	}
	fields := strings.Fields(string(stat[i+1:]))
	// fields[0] is field 3 (state); starttime is field 22 → index 19.
	if len(fields) < 20 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[19], 10, 64)
	return v
}

func parseUID(status []byte) int {
	for _, line := range strings.Split(string(status), "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				uid, err := strconv.Atoi(f[0])
				if err == nil {
					return uid
				}
			}
		}
	}
	return -1
}

var unitRe = regexp.MustCompile(`([^/]+\.service)`)

// parseUnit extracts the systemd service from /proc/PID/cgroup.
func parseUnit(cgroup []byte) string {
	for _, line := range strings.Split(string(cgroup), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if strings.Contains(path, "/user@") || strings.Contains(path, "/session-") {
			continue
		}
		ms := unitRe.FindAllString(path, -1)
		if len(ms) > 0 {
			return unescapeUnit(ms[len(ms)-1])
		}
	}
	return ""
}

// unescapeUnit decodes systemd's \xNN escapes.
func unescapeUnit(s string) string {
	if !strings.Contains(s, `\x`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
