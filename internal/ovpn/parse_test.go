package ovpn

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func parse(t *testing.T, s string) *Config {
	t.Helper()
	c, err := Parse(strings.NewReader(s))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTokenize(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"remote vpn.example.com 1194 udp", []string{"remote", "vpn.example.com", "1194", "udp"}},
		{"  --verb   3  ", []string{"--verb", "3"}},
		{`auth-user-pass "my creds.txt"`, []string{"auth-user-pass", "my creds.txt"}},
		{`push "route 10.0.0.0 255.0.0.0"`, []string{"push", "route 10.0.0.0 255.0.0.0"}},
		{`setenv X 'a "b" c'`, []string{"setenv", "X", `a "b" c`}},
		{`ca C:\\certs\\ca.crt`, []string{"ca", `C:\certs\ca.crt`}},
		{`up "/etc/x \"y\""`, []string{"up", `/etc/x "y"`}},
		{"remote host # comment", []string{"remote", "host"}},
		{"remote host ; comment", []string{"remote", "host"}},
		{"key a#b", []string{"key", "a#b"}}, // '#' inside a token is not a comment
		{"# only comment", nil},
		{"", nil},
	}
	for _, c := range cases {
		got, err := tokenize(c.in)
		if err != nil {
			t.Errorf("tokenize(%q): %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("tokenize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if _, err := tokenize(`remote "unterminated`); err == nil {
		t.Error("expected unterminated quote error")
	}
}

func TestParseDirectivesAndInline(t *testing.T) {
	c := parse(t, "\ufeffclient\n--dev tun\nremote a.example 443 tcp\n<ca>\n-----BEGIN CERTIFICATE-----\nAAA\n-----END CERTIFICATE-----\n</ca>\n\nverb 3\n")
	if len(c.Errors) != 0 {
		t.Fatalf("errors: %v", c.Errors)
	}
	if !c.Has("client") || !c.Has("dev") {
		t.Fatal("expected client and dev (leading -- and BOM stripped)")
	}
	ca, ok := c.Inline("ca")
	if !ok || !strings.Contains(ca.Content, "AAA") || ca.Line != 4 {
		t.Fatalf("inline ca = %+v", ca)
	}
	if d, _ := c.Last("verb"); d.Line != 10 || d.Arg(0) != "3" {
		t.Fatalf("verb line tracking wrong: %+v", d)
	}
}

func TestParseErrors(t *testing.T) {
	c := parse(t, "client\n<ca>\nabc\n")
	if len(c.Errors) != 1 || !strings.Contains(c.Errors[0].Msg, "not closed") {
		t.Fatalf("errors = %v", c.Errors)
	}
	c = parse(t, "</key>\nremote 'x\n")
	if len(c.Errors) != 2 {
		t.Fatalf("errors = %v", c.Errors)
	}
}

func TestRemotesDefaults(t *testing.T) {
	c := parse(t, "client\nproto tcp-client\nport 443\nremote a\nremote b 1195\nremote c 53 udp\n<connection>\nremote d 8443\n</connection>\n")
	got := c.Remotes()
	want := []Remote{{"a", 443, "tcp-client"}, {"b", 1195, "tcp-client"}, {"c", 53, "udp"}, {"d", 8443, "tcp-client"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remotes = %+v\nwant %+v", got, want)
	}
}

func TestAuth(t *testing.T) {
	cases := map[string]AuthUserPass{
		"client\n":                                    {},
		"auth-user-pass\n":                            {Required: true, Line: 1},
		"auth-user-pass creds.txt\n":                  {Required: true, File: "creds.txt", Line: 1},
		"auth-user-pass [inline]\n":                   {Required: true, Inline: true, Line: 1},
		"<auth-user-pass>\nu\np\n</auth-user-pass>\n": {Required: true, Inline: true, Line: 1},
	}
	for in, want := range cases {
		if got := parse(t, in).Auth(); got != want {
			t.Errorf("Auth(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestFileRefs(t *testing.T) {
	c := parse(t, "ca ca.crt\ncert /abs/client.crt\n<key>\nx\n</key>\ntls-auth ta.key 1\nauth-user-pass creds.txt\nhttp-proxy proxy 8080 proxyauth.txt basic\ncrl-verify [inline]\n")
	refs := c.FileRefs("/profiles")
	byName := map[string]FileRef{}
	for _, r := range refs {
		byName[r.Directive] = r
	}
	if r := byName["ca"]; r.Resolved != filepath.Join("/profiles", "ca.crt") {
		t.Errorf("ca resolved = %q", r.Resolved)
	}
	if r := byName["cert"]; r.Resolved != "/abs/client.crt" {
		t.Errorf("cert resolved = %q", r.Resolved)
	}
	if r := byName["key"]; !r.Inline {
		t.Errorf("key should be inline")
	}
	if r := byName["crl-verify"]; !r.Inline {
		t.Errorf("crl-verify [inline] should be inline")
	}
	if r := byName["http-proxy"]; r.Path != "proxyauth.txt" {
		t.Errorf("http-proxy auth file = %q", r.Path)
	}
	if r := byName["auth-user-pass"]; r.Path != "creds.txt" {
		t.Errorf("auth-user-pass = %+v", r)
	}
	// "cd" changes the base directory.
	c = parse(t, "cd /etc/openvpn\nca ca.crt\n")
	if r := c.FileRefs("/profiles")[0]; r.Resolved != "/etc/openvpn/ca.crt" {
		t.Errorf("with cd: %q", r.Resolved)
	}
}

func TestScriptsAndDevice(t *testing.T) {
	c := parse(t, "dev tap0\nscript-security 2\nup /etc/openvpn/update-resolv-conf\ndown /etc/openvpn/update-resolv-conf\n")
	if n, typ := c.Device(); n != "tap0" || typ != "tap" {
		t.Errorf("device = %s %s", n, typ)
	}
	if len(c.Scripts()) != 2 || c.ScriptSecurity() != 2 {
		t.Errorf("scripts = %+v level %d", c.Scripts(), c.ScriptSecurity())
	}
	if !parse(t, "tls-client\npull\n").IsClient() || parse(t, "tls-client\n").IsClient() {
		t.Error("IsClient")
	}
}

func TestDirectiveString(t *testing.T) {
	d := Directive{Name: "pull-filter", Args: []string{"ignore", "route-ipv6 ::/0"}}
	if got := d.String(); got != `pull-filter ignore "route-ipv6 ::/0"` {
		t.Errorf("String() = %s", got)
	}
}
