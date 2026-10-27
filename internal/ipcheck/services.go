package ipcheck

// IPService is one HTTP(S) endpoint returning the caller's address.
type IPService struct {
	Name    string
	URL     string
	Enabled bool
}

// DefaultServices are the endpoints queried when the user has not
// configured any.
func DefaultServices() []IPService {
	return []IPService{
		{Name: "ip.me", URL: "https://ip.me", Enabled: true},
		{Name: "ifconfig.me", URL: "https://ifconfig.me/ip", Enabled: true},
		{Name: "ipify", URL: "https://api.ipify.org", Enabled: true},
		{Name: "icanhazip", URL: "https://icanhazip.com", Enabled: true},
	}
}
