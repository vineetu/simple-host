package sitestest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// uniq is prefix with a suffix no other run shares.
func uniq(prefix string) string { return prefix + strconv.FormatInt(time.Now().UnixNano(), 36) }

// limits loads the env settings in m into a config.Limits value.
func limits(t *testing.T, m map[string]string) config.Limits {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// SetLimits replaces Sites' limits snapshot for the rest of the test.
func (a *App) SetLimits(t *testing.T, m map[string]string) { a.Sites.Limits = limits(t, m) }

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
	a.Sites.ApplyDomainCheck(context.Background(), model.BoundDomain{
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

// tarGzOf is a gzipped tar holding index.html plus a file of n zero bytes:
// a few KB on the wire, n bytes unpacked.
func tarGzOf(t *testing.T, n int) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"index.html": []byte("<h1>big</h1>"), "big.bin": make([]byte, n)} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// tarNames are the entry names of a gzipped tar.
func tarNames(t *testing.T, b []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names[hdr.Name] = true
	}
	return names
}
