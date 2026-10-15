package netguard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCheckURL(t *testing.T) {
	tests := []struct {
		name string
		p    Policy
		url  string
		ok   bool
	}{
		{"https ok", Policy{}, "https://api.x.com/v1", true},
		{"http denied", Policy{}, "http://api.x.com/v1", false},
		{"http allowed", Policy{AllowHTTP: true}, "http://localhost:8080", true},
		{"ftp denied", Policy{AllowHTTP: true}, "ftp://x.com", false},
		{"file denied", Policy{AllowHTTP: true}, "file:///etc/passwd", false},
		{"empty host", Policy{}, "https:///path", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.CheckURL(mustURL(t, tc.url))
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, ErrBlocked) {
				t.Fatalf("err %v does not wrap ErrBlocked", err)
			}
		})
	}
}

func TestMayAuth(t *testing.T) {
	p := Policy{AuthHosts: []string{"api.x.com", ".y.com"}}
	tests := []struct {
		url  string
		want bool
	}{
		{"https://api.x.com/a", true},
		{"https://API.X.COM:443/a", true},
		{"https://api.x.com./a", true},
		{"https://x.com/a", false},
		{"https://evil-api.x.com/a", false},
		{"https://api.x.com.evil.com/a", false},
		{"https://a.y.com/a", true},
		{"https://a.b.y.com/a", true},
		{"https://y.com/a", false}, // ".y.com" is a subdomain-only suffix
		{"https://evil-y.com/a", false},
		{"https://evily.com/a", false},
		{"http://api.x.com/a", false}, // scheme not ok
		{"https://u:p@api.x.com@evil.com/a", false},
	}
	for _, tc := range tests {
		if got := p.MayAuth(mustURL(t, tc.url)); got != tc.want {
			t.Errorf("MayAuth(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
	if (Policy{}).MayAuth(mustURL(t, "https://api.x.com")) {
		t.Error("empty AuthHosts must never match")
	}
	if !(Policy{AuthHosts: []string{"localhost"}, AllowHTTP: true}).MayAuth(mustURL(t, "http://localhost:9")) {
		t.Error("AllowHTTP policy should auth over http")
	}
}

func TestBlockedIPs(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "10.0.0.1", "172.16.5.5", "192.168.1.1", "169.254.169.254",
		"0.0.0.0", "224.0.0.1", "100.64.0.1", "100.127.255.255", "192.0.0.8",
		"198.18.0.1", "198.19.255.255", "240.0.0.1", "255.255.255.255",
		"::1", "::", "fe80::1", "ff02::1", "fc00::1", "fd12::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::7f00:1", "64:ff9b::a00:1",
		"64:ff9b::a9fe:a9fe",
	}
	for _, s := range blocked {
		if err := checkIP(mustAddr(t, s)); !errors.Is(err, ErrBlocked) {
			t.Errorf("checkIP(%s) = %v, want ErrBlocked", s, err)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "100.63.255.255", "100.128.0.1", "198.17.0.1", "198.20.0.1", "2606:4700::1111", "64:ff9b::808:808"}
	for _, s := range allowed {
		if err := checkIP(mustAddr(t, s)); err != nil {
			t.Errorf("checkIP(%s) = %v, want nil", s, err)
		}
	}
}

func TestDialerBlocksLoopbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer srv.Close()
	c := NewClient(Policy{AllowHTTP: true}, 5*time.Second)
	resp, err := get(c, srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("loopback dial must be blocked by default")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestDialerAllowsLoopbackWithAllowPrivate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer srv.Close()
	c := NewClient(Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	resp, err := get(c, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestSchemeBlockedOnFirstRequest(t *testing.T) {
	c := NewClient(Policy{AllowPrivate: true}, 5*time.Second)
	resp, err := get(c, "http://127.0.0.1:1/")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("http must be rejected without AllowHTTP")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestRedirectStripsSecretsOnOtherHost(t *testing.T) {
	var got http.Header
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte("ok"))
	}))
	defer second.Close()
	// 127.0.0.1 and localhost are different hosts for MayAuth purposes.
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(second.URL, "127.0.0.1", "localhost", 1), http.StatusFound)
	}))
	defer first.Close()

	c := NewClient(Policy{AllowHTTP: true, AllowPrivate: true, AuthHosts: []string{"127.0.0.1"}}, 5*time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, first.URL, http.NoBody)
	for _, h := range SecretHeaders {
		req.Header.Set(h, "secret")
	}
	req.Header.Set("X-Other", "keep")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, h := range SecretHeaders {
		if v := got.Get(h); v != "" {
			t.Errorf("header %s leaked to second host: %q", h, v)
		}
	}
	if got.Get("X-Other") != "keep" {
		t.Errorf("non-secret header dropped: %v", got)
	}
}

func TestRedirectKeepsSecretsOnAuthHost(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		got = r.Header.Get("Authorization")
	}))
	defer srv.Close()
	c := NewClient(Policy{AllowHTTP: true, AllowPrivate: true, AuthHosts: []string{"127.0.0.1"}}, 5*time.Second)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/start", http.NoBody)
	req.Header.Set("Authorization", "Bearer k")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got != "Bearer k" {
		t.Fatalf("Authorization = %q, want kept on same auth host", got)
	}
}

func TestRedirectHTTPSToHTTPBlocked(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) }))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer tls.Close()
	c := NewClient(Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	c.Transport.(guard).next.TLSClientConfig = tls.Client().Transport.(*http.Transport).TLSClientConfig
	resp, err := get(c, tls.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("https->http redirect must be blocked")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestRedirectToDisallowedSchemeBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/x", http.StatusFound)
	}))
	defer srv.Close()
	c := NewClient(Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	resp, err := get(c, srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("ftp redirect must be blocked")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestRedirectLimit(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer srv.Close()
	c := NewClient(Policy{AllowHTTP: true, AllowPrivate: true}, 5*time.Second)
	resp, err := get(c, srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("redirect loop must stop")
	}
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if n != 6 { // initial request + 5 hops
		t.Fatalf("server saw %d requests, want 6", n)
	}
}

func TestTimeoutAppliesToWholeRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	c := NewClient(Policy{AllowHTTP: true, AllowPrivate: true}, 50*time.Millisecond)
	if c.Timeout != 50*time.Millisecond {
		t.Fatalf("Timeout = %v", c.Timeout)
	}
	resp, err := get(c, srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected timeout")
	}
}

func get(c *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func TestPrivateHostsCheckURL(t *testing.T) {
	p := Policy{PrivateHosts: []string{"127.0.0.1", "Local.Example"}}
	for url, ok := range map[string]bool{
		"http://127.0.0.1:8080/x":    true,
		"http://local.example/x":     true,
		"https://127.0.0.1/x":        true,
		"http://127.0.0.2/x":         false,
		"http://other.example/x":     false,
		"http://sub.local.example/x": false,
	} {
		if err := p.CheckURL(mustURL(t, url)); (err == nil) != ok {
			t.Errorf("%s: err=%v want ok=%v", url, err, ok)
		}
	}
}

func TestPrivateHostsDialOnlyListedHost(t *testing.T) {
	listed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer listed.Close()
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	l, err := listenLoopback2()
	if err != nil {
		t.Skip("127.0.0.2 not available:", err)
	}
	other.Listener = l
	other.Start()
	defer other.Close()

	c := NewClient(Policy{PrivateHosts: []string{"127.0.0.1"}}, 5*time.Second)
	if err := fetchErr(c, listed.URL); err != nil {
		t.Fatalf("listed private host refused: %v", err)
	}
	err = fetchErr(c, other.URL) // http://127.0.0.2:port
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("unlisted private host reachable: err=%v", err)
	}
	// An https URL on an unlisted private IP is refused by the dialer.
	err = fetchErr(c, strings.Replace(other.URL, "http://", "https://", 1))
	if err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("unlisted private https host reachable: err=%v", err)
	}
}

func TestPrivateHostsRedirectToOtherPrivateHostRefused(t *testing.T) {
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("secret")) }))
	l, err := listenLoopback2()
	if err != nil {
		t.Skip("127.0.0.2 not available:", err)
	}
	target.Listener = l
	target.Start()
	defer target.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer src.Close()
	c := NewClient(Policy{PrivateHosts: []string{"127.0.0.1"}}, 5*time.Second)
	if err := fetchErr(c, src.URL); err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("redirect to another private host followed: err=%v", err)
	}
}
