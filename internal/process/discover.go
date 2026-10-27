// Package process discovers, launches and signals OpenVPN processes.
package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Proc describes a running OpenVPN process.
type Proc struct {
	PID        int
	UID        int
	Args       []string
	StartTicks uint64
	// Config is the --config argument made absolute where possible.
	Config string
	// Socket is the management unix socket, if any.
	Socket string
	// Log is the --log/--log-append target, if any.
	Log string
	// Unit is the systemd unit the process belongs to, if any.
	Unit string
	// Server is true for OpenVPN server instances (mode server / server
	// directive, or an openvpn-server@ unit). They are not client
	// connections and are only listed for information.
	Server bool
}

// Name returns a human label for the process.
func (p Proc) Name() string {
	switch {
	case p.Unit != "":
		return p.Unit
	case p.Config != "":
		return filepath.Base(p.Config)
	default:
		return fmt.Sprintf("pid %d", p.PID)
	}
}

// isServer guesses whether the process is an OpenVPN server. The config
// file is only consulted when it is readable by the current user.
func isServer(p Proc) bool {
	if strings.HasPrefix(p.Unit, "openvpn-server@") {
		return true
	}
	for i, a := range p.Args {
		if a == "--server" || a == "--server-bridge" || (a == "--mode" && i+1 < len(p.Args) && p.Args[i+1] == "server") {
			return true
		}
	}
	if p.Config == "" {
		return false
	}
	data, err := os.ReadFile(p.Config)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch strings.TrimPrefix(f[0], "--") {
		case "server", "server-bridge", "server-ipv6":
			return true
		case "mode":
			if len(f) > 1 && f[1] == "server" {
				return true
			}
		}
	}
	return false
}

func parseArgs(p *Proc) {
	cd := ""
	for i := 0; i < len(p.Args); i++ {
		a := p.Args[i]
		next := func() string {
			if i+1 < len(p.Args) {
				i++
				return p.Args[i]
			}
			return ""
		}
		switch a {
		case "--cd":
			cd = next()
		case "--config":
			p.Config = next()
		case "--management":
			host := next()
			if i+1 < len(p.Args) && p.Args[i+1] == "unix" {
				p.Socket = host
				i++
			}
		case "--log", "--log-append":
			if p.Log == "" {
				p.Log = next()
			} else {
				next()
			}
		}
	}
	if p.Config != "" && !filepath.IsAbs(p.Config) && cd != "" {
		p.Config = filepath.Join(cd, p.Config)
	}
	if p.Socket != "" && !filepath.IsAbs(p.Socket) && cd != "" {
		p.Socket = filepath.Join(cd, p.Socket)
	}
}
