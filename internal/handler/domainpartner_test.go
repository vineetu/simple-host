package handler

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

func TestDomainPartner(t *testing.T) {
	for in, want := range map[string]string{
		"brand.com":          "www.brand.com",
		"www.brand.com":      "brand.com",
		"brand.co.uk":        "www.brand.co.uk",
		"www.brand.co.uk":    "brand.co.uk",
		"shop.brand.com":     "",
		"www.shop.brand.com": "",
	} {
		if got := domainPartner(in); got != want {
			t.Fatalf("domainPartner(%q) = %q, want %q", in, got, want)
		}
	}
}

// The request names the partner on line 3; the ready marker's line 1 is read
// back as the partner's state; a live domain whose partner is not set up asks
// again once the partner points here and the issuer's wait is over.
func TestDomainPartnerHandOff(t *testing.T) {
	h := &SiteHandler{cnameTarget: "cname.simple-host.test", customDomainIP: "203.0.113.7"}
	if p := h.partnerInfoFor("brand.com"); p != nil {
		t.Fatalf("partner offered without the issuer: %+v", p)
	}
	dir := t.TempDir()
	for _, sub := range []string{"requests", "ready", "failed"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.SetDomainCerts(dir)
	d := db.BoundDomain{Domain: "brand.com", UserID: "u", Name: "shop", Token: "sh-0123456789abcdef0123456789abcdef"}
	if body := domainCertRequestBody(d); !strings.HasSuffix(body, "\nwww.brand.com\n") {
		t.Fatalf("request body: %q", body)
	}
	if body := domainCertRequestBody(db.BoundDomain{Domain: "shop.brand.com", UserID: "u", Name: "s", Token: d.Token}); strings.Count(body, "\n") != 2 {
		t.Fatalf("no partner for a subdomain: %q", body)
	}

	if p := h.partnerInfoFor("brand.com"); p == nil || p.Status != "pending" || p.DNS == nil || p.DNS.Type != "CNAME" || p.DNS.Host != "www.brand.com" {
		t.Fatalf("before ready: %+v", p)
	}
	if p := h.partnerInfoFor("www.brand.com"); p == nil || p.DNS.Type != "A" || p.DNS.Value != "203.0.113.7" {
		t.Fatalf("apex partner record: %+v", p)
	}
	ready := filepath.Join(dir, "ready", "brand.com")
	if err := os.WriteFile(ready, []byte("partner www.brand.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := h.partnerInfoFor("brand.com"); p.Status != "live" || p.Note != "" {
		t.Fatalf("live: %+v", p)
	}
	if err := os.WriteFile(ready, []byte("partner-not-set-up www.brand.com: www.brand.com does not point to this server yet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := h.partnerInfoFor("brand.com"); p.Status != "not_set_up" || !strings.Contains(p.Note, "does not point") {
		t.Fatalf("not set up: %+v", p)
	}

	ours := map[string]bool{"203.0.113.7": true}
	orig := lookupHost
	t.Cleanup(func() { lookupHost = orig })
	lookupHost = func(_ context.Context, host string) ([]string, error) { return []string{"203.0.113.7"}, nil }
	// The issuer just wrote the marker: wait.
	if h.wantPartnerCert(context.Background(), d, ours) {
		t.Fatal("asked again before the issuer's wait is over")
	}
	old := time.Now().Add(-partnerRetryAfter - time.Minute)
	if err := os.Chtimes(ready, old, old); err != nil {
		t.Fatal(err)
	}
	if !h.wantPartnerCert(context.Background(), d, ours) {
		t.Fatal("partner points here and the wait is over: should ask again")
	}
	lookupHost = func(_ context.Context, host string) ([]string, error) { return []string{"198.51.100.1"}, nil }
	if h.wantPartnerCert(context.Background(), d, ours) {
		t.Fatal("partner points elsewhere: should not ask")
	}
	// A pending request is kept until the issuer takes it.
	if err := os.WriteFile(filepath.Join(dir, "requests", "brand.com"), []byte(domainCertRequestBody(d)), 0o644); err != nil {
		t.Fatal(err)
	}
	if !h.wantPartnerCert(context.Background(), d, ours) {
		t.Fatal("pending request dropped")
	}
	if err := os.WriteFile(ready, []byte("partner www.brand.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if h.wantPartnerCert(context.Background(), d, ours) {
		t.Fatal("live partner: nothing to ask")
	}
}
