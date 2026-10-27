package backend

// Event names emitted to the frontend.
const (
	EventState    = "connection:state"
	EventTraffic  = "connection:traffic"
	EventNotice   = "app:notice"
	EventLogs     = "logs:append"
	EventLogsClr  = "logs:reset"
	EventProfiles = "profiles:changed"
	EventImport   = "profiles:imported"
)
