package handler

import (
	"context"
	"net"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

// A bare domain still pointed at an earlier address of this server proves
// control; one pointed anywhere else does not.
func TestProveCustomDomainControlAcceptsEarlierAddress(t *testing.T) {
	resolveTo := "198.51.100.9"
	h := &OAuthHandler{
		cfg:         config.Config{CNAMETarget: "cname.simple-host.test", CustomDomainIP: "203.0.113.7", CustomDomainIPAlso: []string{"198.51.100.9"}},
		lookupIP:    func(context.Context, string) ([]net.IP, error) { return []net.IP{net.ParseIP(resolveTo)}, nil },
		lookupCNAME: func(context.Context, string) (string, error) { return "", &net.DNSError{IsNotFound: true} },
	}
	for ip, ok := range map[string]bool{"198.51.100.9": true, "203.0.113.7": true, "192.0.2.1": false} {
		resolveTo = ip
		if err := h.proveCustomDomainControl(context.Background(), "brand.example"); (err == nil) != ok {
			t.Errorf("apex at %s: err=%v, want ok=%v", ip, err, ok)
		}
		if err := h.proveCustomDomainControl(context.Background(), "www.brand.example"); (err == nil) != ok {
			t.Errorf("subdomain at %s: err=%v, want ok=%v", ip, err, ok)
		}
	}
}
