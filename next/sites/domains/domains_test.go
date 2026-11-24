package domains_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"simplehost/app"
	"simplehost/core/testkit"
	"simplehost/core/web"
	"simplehost/sites/domains"
)

// pages stands in for page serving (another package): the site's name.
func pages(a *app.App) {
	a.Routes.Site.Handle("GET /", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		s, err := a.Sites.Of(r)
		if err == nil {
			w.Write([]byte(s.Name))
		}
		return err
	}))
}

const here = "203.0.113.7" // the service's address: simple-host.app's A record

// certFor reads the certificate written for name and checks it names it.
func certFor(t *testing.T, k *testkit.Kit, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(k.App.Config.DataDir, "certs", name, "fullchain.pem"))
	if err != nil {
		t.Fatalf("no certificate for %s: %v", name, err)
	}
	block, _ := pem.Decode(b)
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil || c.VerifyHostname(name) != nil {
		t.Fatalf("certificate for %s: %v", name, err)
	}
	if _, err := os.Stat(filepath.Join(k.App.Config.DataDir, "certs", name, "privkey.pem")); err != nil {
		t.Fatal(err)
	}
}

// SPEC test 18 (F13): an own domain shows the missing records, then goes
// live with a certificate when DNS is right. www redirects to it.
func TestOwnDomain(t *testing.T) {
	k := testkit.Start(t, pages, domains.Mount)
	ctx, a, b := context.Background(), k.App, k.Browser()
	k.DNS.Set("simple-host.app", "A", here)
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "bakery", map[string]string{"index.html": "hi"})
	site, _ := a.Sites.Get(ctx, bob.ID, "bakery")

	d, err := domains.Connect(ctx, a, site, "Bakery-Shop.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "waiting" || len(d.Records) != 3 || d.Records[0].Type != "A" || d.Records[0].Value != here ||
		d.Records[1].Type != "TXT" || d.Records[0].OK || d.Records[1].OK || !d.Records[2].Optional {
		t.Fatalf("before DNS: %+v", d)
	}
	if r := testkit.Call(t, b, "GET", "https://bakery-shop.com/", nil); r.Status != 404 {
		t.Fatalf("unproven domain answered: %d", r.Status)
	}

	k.DNS.Set("bakery-shop.com", "A", here)
	k.DNS.Set("_simple-host.bakery-shop.com", "TXT", d.Records[1].Value)
	if err := domains.Run(ctx, a); err != nil {
		t.Fatal(err)
	}
	ds, err := domains.OfSite(ctx, a, site)
	if err != nil || len(ds) != 1 || ds[0].Status != "live" || ds[0].Expires == nil {
		t.Fatalf("after DNS: %+v %v", ds, err)
	}
	certFor(t, k, "bakery-shop.com")
	if r := testkit.Call(t, b, "GET", "https://bakery-shop.com/", nil); r.Status != 200 || r.Body != "bakery" {
		t.Fatalf("live domain: %d %s", r.Status, r.Body)
	}

	// The www partner redirects once its record is there.
	k.DNS.Set("www.bakery-shop.com", "A", here)
	if err := domains.Run(ctx, a); err != nil {
		t.Fatal(err)
	}
	certFor(t, k, "www.bakery-shop.com")
	if r := testkit.Call(t, b, "GET", "https://www.bakery-shop.com/menu?day=1", nil); r.Status != 301 || r.Header.Get("Location") != "https://bakery-shop.com/menu?day=1" {
		t.Fatalf("www: %d %s", r.Status, r.Header.Get("Location"))
	}

	// Nobody else can take a proven domain.
	eve := k.Person(t, "eve")
	k.Publish(t, eve, "fake", map[string]string{"index.html": "x"})
	fake, _ := a.Sites.Get(ctx, eve.ID, "fake")
	var ref *web.Refusal
	if _, err := domains.Connect(ctx, a, fake, "bakery-shop.com"); !errors.As(err, &ref) || ref.Code != "domain_taken" {
		t.Fatalf("taken domain: %v", err)
	}

	// Removing it leaves the site's own address working.
	if err := domains.Remove(ctx, a, bob.ID, "bakery-shop.com"); err != nil {
		t.Fatal(err)
	}
	if r := testkit.Call(t, b, "GET", "https://bakery-shop.com/", nil); r.Status != 404 {
		t.Fatalf("removed domain: %d", r.Status)
	}
	if r := testkit.Call(t, b, "GET", "https://bakery--bob.simple-host.site/", nil); r.Status != 200 {
		t.Fatalf("own address: %d", r.Status)
	}
}

// SPEC test 19 (F14): with the shared domain set, a newly published site
// answers at <site>.<domain>, and gets its certificate.
func TestSharedDomain(t *testing.T) {
	k := testkit.Start(t, pages, domains.Mount)
	ctx, a, b := context.Background(), k.App, k.Browser()
	k.DNS.Set("simple-host.app", "A", here)
	cb := k.Person(t, "cb")

	d, err := domains.Share(ctx, a, cb.ID, "cb-travel.com")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != "waiting" || d.Records[0].Host != "*.cb-travel.com" || d.Records[0].Type != "CNAME" || d.Records[0].OK || d.Records[1].OK {
		t.Fatalf("before DNS: %+v", d)
	}
	k.DNS.Set("*.cb-travel.com", "A", here)
	k.DNS.Set("_simple-host.cb-travel.com", "TXT", d.Records[1].Value)
	if ds, err := domains.OfOwner(ctx, a, cb.ID); err != nil || len(ds) != 1 || ds[0].Status != "live" {
		t.Fatalf("after DNS: %+v %v", ds, err)
	}

	k.Publish(t, cb, "goa", map[string]string{"index.html": "hi"})
	if r := testkit.Call(t, b, "GET", "https://goa.cb-travel.com/", nil); r.Status != 200 || r.Body != "goa" {
		t.Fatalf("new site: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", "https://kerala.cb-travel.com/", nil); r.Status != 404 {
		t.Fatalf("no such site: %d", r.Status)
	}
	if err := domains.Run(ctx, a); err != nil {
		t.Fatal(err)
	}
	certFor(t, k, "goa.cb-travel.com")
}
