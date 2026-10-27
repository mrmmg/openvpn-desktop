package backend

// Result is returned by actions that can fail for reasons the UI should
// explain. Code lets the UI react (e.g. ask for confirmation); Message is
// always safe to show to the user.
type Result struct {
	OK      bool   `json:"ok"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Result codes.
const (
	CodeBusy          = "busy"
	CodeExternal      = "external"
	CodeNotFound      = "not-found"
	CodeBinaryMissing = "binary-missing"
	CodeInvalid       = "invalid-profile"
	CodeScripts       = "scripts"
	CodeCancelled     = "cancelled"
	CodeError         = "error"
)

func ok() Result                            { return Result{OK: true} }
func fail(code, msg string) Result          { return Result{Code: code, Message: msg} }
func failErr(code string, err error) Result { return fail(code, err.Error()) }

// Profile is a profile as shown in the list. Nothing secret is included.
type Profile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	File     string `json:"file"`
	Server   string `json:"server"`
	Protocol string `json:"protocol"`
	// Auth is "none", "prompt" (asked on connect) or "file" (credential file).
	Auth          string   `json:"auth"`
	Risks         []string `json:"risks"`
	LastConnected int64    `json:"lastConnected"`
	Problem       string   `json:"problem"`
}

// ProfileDetails is the read-only configuration summary.
type ProfileDetails struct {
	Profile
	Directory string   `json:"directory"`
	Remotes   []string `json:"remotes"`
	Device    string   `json:"device"`
	Files     []string `json:"files"`
	Missing   []string `json:"missing"`
	Scripts   []string `json:"scripts"`
	Routing   []string `json:"routing"`
}

// ImportOutcome reports an import to the UI.
type ImportOutcome struct {
	Result
	File    string   `json:"file"`
	Profile *Profile `json:"profile"`
	Notes   []string `json:"notes"`
}

// CredentialRequest is a pending credential prompt from OpenVPN.
type CredentialRequest struct {
	Type         string `json:"type"`
	NeedUsername bool   `json:"needUsername"`
	Challenge    string `json:"challenge"`
}

// ConnectionState is everything the UI shows about the connection. Empty
// strings / zero values mean "not available".
type ConnectionState struct {
	// State: disconnected, connecting, connected, disconnecting, error, unknown.
	State  string `json:"state"`
	Detail string `json:"detail"`
	// ActiveID is the profile the connection belongs to ("" when idle).
	ActiveID string `json:"activeId"`
	// Starting is true while the app is launching OpenVPN (authorization
	// dialog, validation); controls must not be used meanwhile.
	Starting bool `json:"starting"`

	ConnectedSince int64 `json:"connectedSince"`
	PhaseSince     int64 `json:"phaseSince"`

	Interface  string   `json:"interface"`
	VPNIP      string   `json:"vpnIp"`
	ServerHost string   `json:"serverHost"`
	ServerPort string   `json:"serverPort"`
	ServerIP   string   `json:"serverIp"`
	Protocol   string   `json:"protocol"`
	Cipher     string   `json:"cipher"`
	Auth       string   `json:"auth"`
	DNS        []string `json:"dns"`
	Routes     []string `json:"routes"`
	// PublicIP is only set when a lookup through the tunnel succeeded and
	// all services agreed. PublicIPState: off, idle, checking, ok, failed.
	PublicIP      string `json:"publicIp"`
	PublicIPState string `json:"publicIpState"`

	BytesIn  uint64 `json:"bytesIn"`
	BytesOut uint64 `json:"bytesOut"`

	Error      string `json:"error"`
	ErrorHint  string `json:"errorHint"`
	AuthFailed bool   `json:"authFailed"`
	Message    string `json:"message"`
	Reconnects int    `json:"reconnects"`

	Credentials *CredentialRequest `json:"credentials"`
	// External describes OpenVPN processes this app does not control.
	External []string `json:"external"`
}

// TrafficSample is one second of measured throughput (bytes/s) with the
// cumulative totals of the current session.
type TrafficSample struct {
	Time      int64   `json:"t"`
	Down      float64 `json:"down"`
	Up        float64 `json:"up"`
	TotalDown uint64  `json:"totalDown"`
	TotalUp   uint64  `json:"totalUp"`
}

// Notice is a short message shown as a toast.
type Notice struct {
	// Level: info, success, warning, error.
	Level   string `json:"level"`
	Title   string `json:"title"`
	Message string `json:"message"`
}

// LogEntry is one line of the log view.
type LogEntry struct {
	ID int64 `json:"id"`
	// Time is the OpenVPN timestamp when known, else the local time (RFC 3339).
	Time string `json:"time"`
	// Level: debug, info, warn, error.
	Level string `json:"level"`
	// Source: openvpn or app.
	Source  string `json:"source"`
	Message string `json:"message"`
}

// Settings is the editable configuration.
type Settings struct {
	Theme         string `json:"theme"`
	LogVerbosity  int    `json:"logVerbosity"`
	OpenVPNBinary string `json:"openvpnBinary"`
	// DetectedBinary is what auto-detection finds ("" when nothing).
	DetectedBinary    string      `json:"detectedBinary"`
	ProfilesDir       string      `json:"profilesDir"`
	PublicIPCheck     bool        `json:"publicIpCheck"`
	IPServices        []IPService `json:"ipServices"`
	AutoReconnect     bool        `json:"autoReconnect"`
	ReconnectAttempts int         `json:"reconnectAttempts"`
	ReconnectInterval int         `json:"reconnectInterval"`
	WindowWidth       int         `json:"windowWidth"`
	WindowHeight      int         `json:"windowHeight"`
	ConfigFile        string      `json:"configFile"`
	LogFile           string      `json:"logFile"`
	Privilege         string      `json:"privilege"`
	Version           string      `json:"version"`
}

// AboutInfo is shown on the About page.
type AboutInfo struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	OpenVPN   string   `json:"openvpn"`
	Platform  string   `json:"platform"`
	License   string   `json:"license"`
	Notices   []string `json:"notices"`
	ConfigDir string   `json:"configDir"`
}

// IPService is one public-IP lookup endpoint.
type IPService struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}
