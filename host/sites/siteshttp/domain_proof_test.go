package siteshttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
)

// The status probe connects only to the address it was given and never
// follows a redirect; only public addresses count as this server.
func TestDomainProbePinnedNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/", http.StatusFound)
	}))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	res, err := domainProbeVia(host).Get("http://pinned.example.invalid:" + port + "/")
	if err != nil {
		t.Fatalf("pinned dial: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("redirect followed: %d", res.StatusCode)
	}
	for ip, want := range map[string]bool{"203.0.113.7": true, "147.224.49.228": true, "127.0.0.1": false, "10.0.0.1": false, "169.254.169.254": false,
		"100.64.1.1": false, "::1": false, "fd00::1": false, "0.0.0.0": false, "2606:4700::1111": true, "::ffff:10.0.0.1": false, "bogus": false} {
		if got := isPublicIP(ip); got != want {
			t.Errorf("isPublicIP(%s) = %v", ip, got)
		}
	}
	// A domain resolving only to a private address is not "here".
	h := withHosts(&SiteHandler{})
	old := sites.LookupHost
	defer func() { sites.LookupHost = old }()
	sites.LookupHost = func(context.Context, string) ([]string, error) { return []string{"127.0.0.1"}, nil }
	if st, _, here := h.verifyDomain(context.Background(), "x.example.test", map[string]bool{"127.0.0.1": true}); st != "pending" || here {
		t.Fatalf("private address counted as here: %s %v", st, here)
	}
	sites.LookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("nx") }
	if st, _, _ := h.verifyDomain(context.Background(), "x.example.test", nil); st != "pending" {
		t.Fatalf("unresolvable: %s", st)
	}
}

// After an address change, domains still pointed at an earlier address
// (CUSTOM_DOMAIN_IP_ALSO) count as here, and only CUSTOM_DOMAIN_IP is handed
// out as the A record.
func TestEarlierServerAddressStillCounts(t *testing.T) {
	old := sites.LookupHost
	defer func() { sites.LookupHost = old }()
	sites.LookupHost = func(context.Context, string) ([]string, error) { return nil, errors.New("nx") }
	h := withHosts(&SiteHandler{CustomDomainIP: "203.0.113.7", CNAMETarget: "cname.simple-host.test"})
	h.Hosts.SetCustomDomainIPAlso([]string{"198.51.100.9"})
	ours := h.Hosts.ServerAddrs(context.Background())
	if !ours["203.0.113.7"] || !ours["198.51.100.9"] || len(ours) != 2 {
		t.Fatalf("serverAddrs = %v", ours)
	}
	if rec := h.dnsRecordFor("brand.example"); rec.Type != "A" || rec.Value != "203.0.113.7" {
		t.Fatalf("handed out %+v, want the current address", rec)
	}
}
