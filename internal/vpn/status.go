// Package vpn is the connection manager. It starts and stops OpenVPN,
// follows it through the management interface, cross-checks the kernel
// (process table, interfaces) and produces a single Status for the UI.
package vpn

import (
	"time"

	"github.com/mrmmg/openvpn-desktop/internal/logs"
	"github.com/mrmmg/openvpn-desktop/internal/mgmt"
	"github.com/mrmmg/openvpn-desktop/internal/network"
	"github.com/mrmmg/openvpn-desktop/internal/process"
)

// Phase is the user-facing connection state.
type Phase string

// Phases.
const (
	Disconnected  Phase = "DISCONNECTED"
	Connecting    Phase = "CONNECTING"
	Connected     Phase = "CONNECTED"
	Disconnecting Phase = "DISCONNECTING"
	Failed        Phase = "ERROR"
	Unknown       Phase = "UNKNOWN"
)

// Source says who controls the observed connection.
type Source string

// Sources.
const (
	SourceNone     Source = ""
	SourceManaged  Source = "managed"  // started by openvpn-desktop, controlled via management socket
	SourceExternal Source = "external" // another openvpn process
	SourceSystemd  Source = "systemd"  // an openvpn systemd unit
)

// Status is a snapshot of the connection state.
type Status struct {
	Phase      Phase
	PhaseSince time.Time
	// Detail is OpenVPN's state name/description (e.g. "AUTH", "GET_CONFIG").
	Detail string
	Source Source

	Profile     string
	ProfilePath string
	PID         int
	Unit        string
	// Attached is true while the management socket is connected.
	Attached bool

	Iface          string
	LocalIP        string
	LocalIPv6      string
	RemoteIP       string
	RemotePort     string
	ConnectedSince time.Time

	BytesIn, BytesOut uint64

	LogPath      string
	OverrideArgs []string

	// Failure explains the last unexpected end or error.
	Failure *logs.Failure
	// Password is a pending credential request from OpenVPN.
	Password *mgmt.PasswordRequest
	// Message is a transient informational message (e.g. web-auth URL).
	Message string

	Reconnects int
	// External lists OpenVPN client processes not managed by this app.
	External []process.Proc
	// Servers lists OpenVPN server instances running on this host.
	Servers []process.Proc
}

// Active reports whether some connection is up or being set up.
func (s Status) Active() bool {
	switch s.Phase {
	case Connecting, Connected, Disconnecting:
		return true
	}
	return false
}

// Snapshot is the result of a refresh: status plus the interfaces used to
// compute it (so the UI does not query them twice).
type Snapshot struct {
	Status   Status
	Links    []network.Link
	VPNLink  *network.Link
	LinksErr error
}

// NoticeKind classifies notable events.
type NoticeKind int

// Notice kinds.
const (
	NoticeConnected NoticeKind = iota
	NoticeDisconnected
	NoticeDropped
	NoticeAuthFailed
	NoticeError
	NoticeReconnecting
	NoticeInfo
)

// Notice is an event worth showing as a toast / desktop notification.
type Notice struct {
	Kind    NoticeKind
	Title   string
	Message string
	At      time.Time
}
