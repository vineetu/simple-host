package papermetrics

import "testing"

func TestIsAuthPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/oauth/token": true,
		"/.well-known/oauth-authorization-server": true,
		"/v1/auth/login":         true,
		"/v1/me/sessions":        true,
		"/v1/sites/blog/me":      true,
		"/v1/me":                 false,
		"/v1/sites/blog/storage": false,
		"/v1/sites/me":           false,
		"/":                      false,
	} {
		if got := isAuthPath(p); got != want {
			t.Errorf("isAuthPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIsPlatformHost(t *testing.T) {
	c := New(nil, "", "simple-host.app")
	for h, want := range map[string]bool{
		"simple-host.app":              true,
		"":                             true,
		"localhost:8080":               true,
		"[::1]:8080":                   true,
		"blog.vineetu.simple-host.app": false,
		"example.com:443":              false,
	} {
		if got := c.isPlatformHost(h); got != want {
			t.Errorf("isPlatformHost(%q) = %v, want %v", h, got, want)
		}
	}
}

func TestIsVersionDir(t *testing.T) {
	for name, want := range map[string]bool{"v1": true, "v42": true, "v": false, "v1a": false, "current": false, "x1": false} {
		if got := isVersionDir(name); got != want {
			t.Errorf("isVersionDir(%q) = %v, want %v", name, got, want)
		}
	}
}
