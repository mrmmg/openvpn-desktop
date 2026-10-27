package validate

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrmmg/openvpn-wails/internal/ovpn"
	"github.com/mrmmg/openvpn-wails/internal/profile"
)

// material loads the PEM text of a file directive, inline or from disk.
func material(cfg *ovpn.Config, dir, name string) (text string, src string, ok bool, err error) {
	if d, found := cfg.Inline(name); found {
		return d.Content, "inline <" + name + ">", true, nil
	}
	for _, ref := range cfg.FileRefs(dir) {
		if ref.Directive != name || ref.Inline {
			continue
		}
		data, rerr := os.ReadFile(ref.Resolved)
		if rerr != nil {
			return "", ref.Path, true, rerr
		}
		return string(data), ref.Path, true, nil
	}
	return "", "", false, nil
}

func parseCerts(text string) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	rest := []byte(text)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return certs, err
		}
		certs = append(certs, c)
	}
	if len(certs) == 0 {
		return nil, errors.New("no PEM certificate found")
	}
	return certs, nil
}

// parseKey returns the public key of a PEM private key. encrypted is true
// for passphrase-protected keys (which cannot be checked without the
// passphrase).
func parseKey(text string) (pub crypto.PublicKey, encrypted bool, err error) {
	rest := []byte(text)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, false, errors.New("no PEM private key found")
		}
		if !strings.Contains(block.Type, "PRIVATE KEY") {
			continue
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || block.Headers["Proc-Type"] == "4,ENCRYPTED" {
			return nil, true, nil
		}
		var key any
		switch block.Type {
		case "RSA PRIVATE KEY":
			key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			key, err = x509.ParseECPrivateKey(block.Bytes)
		default:
			key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
		}
		if err != nil {
			return nil, false, err
		}
		switch k := key.(type) {
		case *rsa.PrivateKey:
			return &k.PublicKey, false, nil
		case *ecdsa.PrivateKey:
			return &k.PublicKey, false, nil
		case ed25519.PrivateKey:
			return k.Public(), false, nil
		default:
			return nil, false, fmt.Errorf("unsupported key type %T", key)
		}
	}
}

type publicKeyEqualer interface{ Equal(crypto.PublicKey) bool }

func (v *Validator) crypto(r *Report, cfg *ovpn.Config, p profile.Profile) {
	const L = LayerCrypto
	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	warnWindow := v.ExpiryWarning
	if warnWindow == 0 {
		warnWindow = 30 * 24 * time.Hour
	}
	checkValidity := func(c *x509.Certificate, what string, expiredSev Severity) {
		subj := c.Subject.CommonName
		if subj == "" {
			subj = c.Subject.String()
		}
		switch {
		case now.After(c.NotAfter):
			r.add(L, expiredSev, what+" expired", fmt.Sprintf("%q expired on %s", subj, c.NotAfter.Format("2006-01-02")), 0)
		case now.Before(c.NotBefore):
			r.add(L, expiredSev, what+" not yet valid", fmt.Sprintf("%q is valid from %s", subj, c.NotBefore.Format("2006-01-02")), 0)
		case c.NotAfter.Sub(now) < warnWindow:
			r.add(L, Warn, what+" expires soon", fmt.Sprintf("%q expires on %s", subj, c.NotAfter.Format("2006-01-02")), 0)
		default:
			r.add(L, OK, what+" valid", fmt.Sprintf("%q until %s", subj, c.NotAfter.Format("2006-01-02")), 0)
		}
	}

	if _, _, ok, _ := material(cfg, p.Dir, "pkcs12"); ok {
		r.add(L, Info, "PKCS#12 bundle", "contents are verified by OpenVPN when connecting", 0)
	}

	// CA
	if text, src, ok, err := material(cfg, p.Dir, "ca"); ok {
		switch {
		case err != nil:
			r.add(L, Skipped, "CA not inspected", fmt.Sprintf("%s: %v", src, err), 0)
		default:
			certs, perr := parseCerts(text)
			if perr != nil {
				r.add(L, Error, "CA certificate invalid", fmt.Sprintf("%s: %v", src, perr), 0)
			} else {
				for _, c := range certs {
					checkValidity(c, "CA certificate", Error)
				}
			}
		}
	} else if !cfg.Has("pkcs12") && !cfg.Has("secret") {
		r.add(L, Error, "no CA certificate", "the profile has neither 'ca' nor 'pkcs12'", 0)
	}

	// Client certificate + key
	var leaf *x509.Certificate
	certText, certSrc, hasCert, certErr := material(cfg, p.Dir, "cert")
	if hasCert {
		if certErr != nil {
			r.add(L, Skipped, "client certificate not inspected", fmt.Sprintf("%s: %v", certSrc, certErr), 0)
		} else if certs, err := parseCerts(certText); err != nil {
			r.add(L, Error, "client certificate invalid", fmt.Sprintf("%s: %v", certSrc, err), 0)
		} else {
			leaf = certs[0]
			checkValidity(leaf, "client certificate", Error)
		}
	}
	keyText, keySrc, hasKey, keyErr := material(cfg, p.Dir, "key")
	if hasKey {
		switch {
		case keyErr != nil:
			r.add(L, Skipped, "private key not inspected", fmt.Sprintf("%s: %v", keySrc, keyErr), 0)
		default:
			pub, encrypted, err := parseKey(keyText)
			switch {
			case err != nil:
				r.add(L, Error, "private key invalid", fmt.Sprintf("%s: %v", keySrc, err), 0)
			case encrypted:
				r.add(L, Info, "private key is encrypted", "the passphrase will be asked when connecting", 0)
			case leaf != nil:
				if eq, ok := pub.(publicKeyEqualer); ok && !eq.Equal(leaf.PublicKey) {
					r.add(L, Error, "private key does not match certificate", keySrc+" ↔ "+certSrc, 0)
				} else {
					r.add(L, OK, "private key matches certificate", "", 0)
				}
			default:
				r.add(L, OK, "private key readable", keySrc, 0)
			}
		}
	}
	if hasCert != hasKey && !cfg.Has("management-external-key") {
		r.add(L, Error, "certificate/key mismatch", "'cert' and 'key' must be used together", 0)
	}
	if !hasCert && !cfg.Has("pkcs12") && !cfg.Has("secret") {
		if cfg.Auth().Required {
			r.add(L, Info, "no client certificate", "username/password authentication only", 0)
		} else {
			r.add(L, Warn, "no client authentication", "no certificate and no auth-user-pass", 0)
		}
	}

	// Static keys
	for _, name := range []string{"tls-auth", "tls-crypt", "tls-crypt-v2", "secret"} {
		text, src, ok, err := material(cfg, p.Dir, name)
		if !ok {
			continue
		}
		if err != nil {
			r.add(L, Skipped, name+" key not inspected", fmt.Sprintf("%s: %v", src, err), 0)
			continue
		}
		marker := "-----BEGIN OpenVPN Static key V1-----"
		if name == "tls-crypt-v2" {
			marker = "-----BEGIN OpenVPN tls-crypt-v2 client key-----"
		}
		if strings.Contains(text, marker) {
			r.add(L, OK, name+" key present", src, 0)
		} else {
			r.add(L, Error, name+" key invalid", src+": missing "+marker, 0)
		}
	}
}
