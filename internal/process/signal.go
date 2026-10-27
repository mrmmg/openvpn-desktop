package process

import (
	"os"
	"syscall"
)

func signalFromName(name string) os.Signal {
	switch name {
	case "KILL", "SIGKILL":
		return syscall.SIGKILL
	case "HUP", "SIGHUP":
		return syscall.SIGHUP
	case "USR1", "SIGUSR1":
		return syscall.SIGUSR1
	default:
		return syscall.SIGTERM
	}
}
