//go:build darwin

package process

import (
	"bufio"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ParsePS parses `ps -axww -o pid=,uid=,lstart=,command=` output (macOS/BSD)
// and returns the openvpn processes. StartTicks holds the start time in Unix
// seconds, which is enough to detect PID reuse.
func ParsePS(out string) []Proc {
	var procs []Proc
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// pid uid Dow Mon DD HH:MM:SS YYYY command...
		if len(f) < 8 {
			continue
		}
		args := f[7:]
		if filepath.Base(args[0]) != "openvpn" {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		uid, err := strconv.Atoi(f[1])
		if err != nil {
			uid = -1
		}
		p := Proc{PID: pid, UID: uid, Args: args}
		if t, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(f[2:7], " "), time.Local); err == nil {
			p.StartTicks = uint64(t.Unix())
		}
		parseArgs(&p)
		p.Server = isServer(p)
		procs = append(procs, p)
	}
	return procs
}
