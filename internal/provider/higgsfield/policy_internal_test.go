package higgsfield

import (
	"net/url"
	"testing"
)

func TestDefaultPolicyIsStrict(t *testing.T) {
	p := New("a", "b").policy()
	if p.AllowPrivate || p.AllowHTTP {
		t.Fatalf("default policy must be strict: %+v", p)
	}
	for raw, want := range map[string]bool{
		"https://api.higgsfield.ai/requests/1/status": true,
		"http://api.higgsfield.ai/x":                  false,
		"https://cdn.higgsfield.ai/x":                 false,
		"https://evil-api.higgsfield.ai.evil.com/x":   false,
	} {
		u, _ := url.Parse(raw)
		if got := p.MayAuth(u); got != want {
			t.Errorf("MayAuth(%s) = %v, want %v", raw, got, want)
		}
	}
	if u, _ := url.Parse("http://storage.example.com/put"); p.CheckURL(u) == nil {
		t.Error("plain http upload must be refused for the default host")
	}
	if u, _ := url.Parse("https://storage.example.com/put"); p.CheckURL(u) != nil {
		t.Error("https upload must pass CheckURL")
	}
}

func TestConfiguredBaseURLPolicy(t *testing.T) {
	c := New("a", "b")
	c.BaseURL = "http://127.0.0.1:9"
	p := c.policy()
	if p.AllowPrivate || p.AllowHTTP || len(p.PrivateHosts) != 1 {
		t.Fatalf("configured base_url should allow local http: %+v", p)
	}
	u, _ := url.Parse("http://localhost:9/x")
	if p.MayAuth(u) {
		t.Error("other host must not get credentials")
	}
}
