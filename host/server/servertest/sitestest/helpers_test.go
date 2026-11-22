package sitestest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/domains"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// SetLimits replaces Sites' limits snapshot for the rest of the test.
func (a *App) SetLimits(t *testing.T, m map[string]string) { a.Sites.Limits = fixture.Limits(t, m) }

// files deploys or updates p's site name with files through the deploy route.
func (a *App) files(t *testing.T, p rt.Person, method, name string, files map[string]string) rt.Resp {
	t.Helper()
	return a.At(t, method, rt.SiteDomain, "/v1/sites/"+name+"/files", map[string]any{"files": files}, p.Header())
}

// api is a request to the API host with p's key.
func (a *App) api(t *testing.T, p rt.Person, method, path string, body any) rt.Resp {
	t.Helper()
	return a.At(t, method, rt.SiteDomain, path, body, p.Header())
}

// admin is a request to the API host with the admin key.
func (a *App) admin(t *testing.T, method, path string, body any) rt.Resp {
	t.Helper()
	return a.At(t, method, rt.SiteDomain, path, body, map[string]string{"X-API-Key": a.Admin})
}

// siteDir is p's site name's directory on disk.
func (a *App) siteDir(p rt.Person, name string) string { return a.Disk.SiteDir(p.ID, name) }

// protect gives siteID a passcode (row and marker), as Access's lock route would.
func (a *App) protect(t *testing.T, p rt.Person, name string) {
	t.Helper()
	rt.SetPasscode(t, a.Database, a.SiteID(t, p, name))
	if err := a.Disk.SetPasscodeMarker(p.ID, name, true); err != nil {
		t.Fatal(err)
	}
}

// suspend and offline set the disk's operator markers on p's site.
func (a *App) suspend(t *testing.T, p rt.Person, name string) {
	t.Helper()
	if err := a.Disk.SetSuspended(p.ID, name, true); err != nil {
		t.Fatal(err)
	}
}

func (a *App) offline(t *testing.T, p rt.Person, name string) {
	t.Helper()
	if err := a.Disk.SetOffline(p.ID, name, true); err != nil {
		t.Fatal(err)
	}
}

func (a *App) domainInfo(t *testing.T, siteID string) model.SiteDomainInfo {
	t.Helper()
	info, _, err := sitesrepo.GetSiteDomainInfo(context.Background(), a.Database, siteID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// verify stands in for a passing domain check (DNS, certificate, HTTPS 2xx).
func (a *App) verify(t *testing.T, siteID, domain string) {
	t.Helper()
	info := a.domainInfo(t, siteID)
	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(context.Background(), model.BoundDomain{
		SiteID: siteID, Domain: domain, Status: info.Status, Verified: info.VerifiedAt.Valid, PreviousDomain: info.PreviousDomain,
	}, "active", "", "live")
}

func (a *App) redirectMarker(t *testing.T, p rt.Person, site string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a.siteDir(p, site), "domain-redirect"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}
