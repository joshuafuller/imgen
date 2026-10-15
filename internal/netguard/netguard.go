// Package netguard restricts where outbound HTTP requests may go and which
// requests may carry credentials. Every HTTP client in imgen is built here.
package netguard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrBlocked is wrapped by every error that netguard raises for policy.
var ErrBlocked = errors.New("netguard: blocked")

// SecretHeaders are removed from a redirected request whose target is not MayAuth.
var SecretHeaders = []string{"Authorization", "X-Goog-Api-Key", "Api-Key", "X-Key", "Proxy-Authorization"}

const maxRedirects = 5

// Policy decides where an outbound request may go.
//
// AuthHosts entries are lower-case host names without port. An entry such as
// "api.x.com" matches that host only. An entry that starts with a dot, such as
// ".x.com", matches every subdomain ("a.x.com", "a.b.x.com") but NOT "x.com"
// itself and NOT look-alikes such as "evil-x.com". List both forms to cover both.
type Policy struct {
	AuthHosts    []string // hosts that may receive credentials: exact "api.x.com" or suffix ".x.com"
	AllowPrivate bool     // permit loopback/private/link-local/reserved IPs (only for user-configured local base_url)
	AllowHTTP    bool     // permit plain http (only for explicit user opt-ins, never a provider base_url)
	// PrivateHosts are request hosts (exact, no port) that may be reached over
	// plain http and at private/loopback addresses. A provider sets it to its
	// configured base_url host: that host is a trust decision, and nothing else
	// (download, upload or poll URLs on other hosts) gets the same latitude.
	PrivateHosts []string
}

// privateHost reports whether host is listed in PrivateHosts.
func (p Policy) privateHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, h := range p.PrivateHosts {
		if h = strings.TrimSuffix(strings.ToLower(h), "."); h != "" && h == host {
			return true
		}
	}
	return false
}

// CheckURL accepts https URLs with a host, and http only if AllowHTTP is set
// or the host is in PrivateHosts.
func (p Policy) CheckURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("%w: nil URL", ErrBlocked)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && (p.AllowHTTP || p.privateHost(u.Hostname())):
	default:
		return fmt.Errorf("%w: scheme %q not allowed", ErrBlocked, u.Scheme)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: empty host", ErrBlocked)
	}
	return nil
}

// MayAuth reports whether credentials may be sent to u.
func (p Policy) MayAuth(u *url.URL) bool {
	if p.CheckURL(u) != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	for _, h := range p.AuthHosts {
		h = strings.TrimSuffix(strings.ToLower(h), ".")
		switch {
		case h == "":
		case strings.HasPrefix(h, "."):
			if len(host) > len(h) && strings.HasSuffix(host, h) {
				return true
			}
		case host == h:
			return true
		}
	}
	return false
}

// NewClient returns a client whose dialer rejects forbidden IPs after DNS
// resolution, whose redirects are re-checked, and whose Timeout covers the
// whole request. It ignores proxy environment variables: a proxy would hide
// the real destination from the dialer.
func NewClient(p Policy, timeout time.Duration) *http.Client {
	// strict rejects forbidden IPs after DNS resolution, so a rebinding name
	// cannot slip through. open is for hosts the policy trusts: AllowPrivate
	// (every host) or PrivateHosts (the request host as written, before DNS).
	strict := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: bad address %q", ErrBlocked, address)
			}
			return checkIP(ap.Addr())
		},
	}
	open := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err == nil && (p.AllowPrivate || p.privateHost(host)) {
			return open.DialContext(ctx, network, addr)
		}
		return strict.DialContext(ctx, network, addr)
	}
	tr := &http.Transport{
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: guard{p: p, next: tr},
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("%w: more than %d redirects", ErrBlocked, maxRedirects)
			}
			if err := p.CheckURL(req.URL); err != nil {
				return err
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: https to http redirect", ErrBlocked)
			}
			if !p.MayAuth(req.URL) {
				for _, h := range SecretHeaders {
					req.Header.Del(h)
				}
			}
			return nil
		},
	}
}

var blockedPrefixes = mustPrefixes(
	"100.64.0.0/10", // CGNAT
	"192.0.0.0/24",  // IETF protocol assignments
	"198.18.0.0/15", // benchmarking
	"240.0.0.0/4",   // reserved (includes broadcast)
	"fc00::/7",      // unique local
)

var nat64 = netip.MustParsePrefix("64:ff9b::/96")

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, v := range s {
		out[i] = netip.MustParsePrefix(v)
	}
	return out
}

// checkIP rejects every address a public image host should never use.
func checkIP(a netip.Addr) error {
	a = a.Unmap()
	if nat64.Contains(a) {
		b := a.As16()
		a = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	bad := a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified()
	if !bad {
		for _, pf := range blockedPrefixes {
			if pf.Contains(a) {
				bad = true
				break
			}
		}
	}
	if bad {
		return fmt.Errorf("%w: address %s is not allowed", ErrBlocked, a)
	}
	return nil
}

// guard checks the scheme and host of every request, including the first.
type guard struct {
	p    Policy
	next *http.Transport
}

func (g guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := g.p.CheckURL(r.URL); err != nil {
		if r.Body != nil {
			_ = r.Body.Close()
		}
		return nil, err
	}
	return g.next.RoundTrip(r)
}
