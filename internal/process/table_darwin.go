//go:build darwin

package process

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

// PSTable reads processes with ps(1); macOS has no /proc.
type PSTable struct{}

// DefaultTable returns the process table of the running platform.
func DefaultTable() PSTable { return PSTable{} }

func (PSTable) run(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "/bin/ps", args...).Output()
	return string(out)
}

// FindOpenVPN lists running openvpn processes.
func (t PSTable) FindOpenVPN() ([]Proc, error) {
	return ParsePS(t.run("-axww", "-o", "pid=,uid=,lstart=,command=")), nil
}

// Inspect returns pid if it is an openvpn process.
func (t PSTable) Inspect(pid int) (Proc, bool) {
	ps := ParsePS(t.run("-ww", "-p", strconv.Itoa(pid), "-o", "pid=,uid=,lstart=,command="))
	if len(ps) != 1 {
		return Proc{}, false
	}
	return ps[0], true
}

// Alive reports whether pid is still the same openvpn process.
func (t PSTable) Alive(pid int, startTicks uint64) bool {
	p, ok := t.Inspect(pid)
	return ok && (startTicks == 0 || p.StartTicks == startTicks)
}
