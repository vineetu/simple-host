package siteshttp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// The platform's setup page offers no product choice: a product adds its own
// through its asset contribution (products/hack/servertest covers Simple Hack's).
func TestSetupHelperPlatformEdition(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
	page := get(t, mux, "example.test", "/setup")
	for _, want := range []string{
		"<title>Set up Simple Host</title>",
		"<h1>Set up Simple Host</h1>",
		`data-setup-product="small"`,
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("/setup missing %q", want)
		}
	}
	if rec := get(t, mux, "example.test", "/setup/simple-hack.json"); rec.Code != 404 {
		t.Fatalf("Host product manifest: %d", rec.Code)
	}
	js := get(t, mux, "example.test", "/setup/setup.js").Body.String()
	for _, absent := range []string{"renderHackBasics", "renderHackOutput", "simple-hack.json", "Simple Hack"} {
		if strings.Contains(js, absent) {
			t.Errorf("platform setup.js names the product: %q", absent)
		}
	}
}

// The reviewable ZIP must receive the exact published dependency bytes, not a
// text-Rewritten/corrupt archive or a different chart version.
func TestSetupHelperBundledEnterpriseChart(t *testing.T) {
	js, err := web.StaticFiles.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	version := regexp.MustCompile(`var ENT_CHART_VERSION = '([^']+)';`).FindSubmatch(js)
	checksum := regexp.MustCompile(`var ENT_CHART_SHA256 = '([a-f0-9]{64})';`).FindSubmatch(js)
	if version == nil || checksum == nil {
		t.Fatal("missing dependency version or checksum")
	}
	path := "/setup/charts/simple-host-enterprise-" + string(version[1]) + ".tgz"
	rec := get(t, chromeTestMux(t, platformTestEdition()), "simple-host.app", path)
	if rec.Code != http.StatusOK {
		t.Fatalf("chart download status: %d", rec.Code)
	}
	data := rec.Body.Bytes()
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != string(checksum[1]) {
		t.Fatalf("served chart checksum: %s", got)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	for {
		header, err := archive.Next()
		if err == io.EOF {
			t.Fatal("dependency Chart.yaml missing")
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name != "simple-host-enterprise/Chart.yaml" {
			continue
		}
		metadata, err := io.ReadAll(archive)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(metadata), "version: "+string(version[1])+"\n") {
			t.Fatal("bundled dependency version differs from setup page")
		}
		break
	}
}

// withHosts gives a handler built by hand the address settings its fields
// name (NewSiteHandlers builds them for real handlers).
func withHosts(h *SiteHandler) *SiteHandler {
	ed := h.Ed
	if ed == nil {
		ed = edition.Default(config.DefaultLimits())
	}
	h.Hosts = sites.NewHosts(h.Database, h.Disk, ed, h.SiteDomain, h.ContentHost, h.CNAMETarget, h.CustomDomainIP)
	return h
}
