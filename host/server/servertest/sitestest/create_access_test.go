package sitestest

import (
	"archive/zip"
	"bytes"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
)

func TestCreateSpecificFirstRequest(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	mom, dad := p.NewPerson(t, "mom"), p.NewPerson(t, "dad")
	for _, tc := range []struct {
		name, method, suffix string
		archive              bool
	}{
		{"jsonprivate", "POST", "/files", false}, {"putprivate", "PUT", "/files?create=1", false},
		{"zipprivate", "POST", "?access=specific&viewers=" + mom.Email, true}, {"putzipprivate", "PUT", "?create=1&access=specific&viewers=" + mom.Email, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body any = map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary}, "access": "specific", "viewers": []string{mom.Email}}
			if tc.archive {
				var b bytes.Buffer
				z := zip.NewWriter(&b)
				f, _ := z.Create("index.html")
				_, _ = f.Write([]byte(servertest.PasscodeCanary))
				_ = z.Close()
				body = b.String()
			}
			r := p.At(t, tc.method, servertest.PcSiteDomain, "/v1/sites/"+tc.name+tc.suffix, body, p.Okey)
			if r.Status != 201 || r.JSON(t)["access"] != "specific" {
				t.Fatalf("create: %d %s", r.Status, r.Body)
			}
			uid, _ := p.UserID(t, p.Olive)
			if !p.Sites.Disk.HasPasscodeMarker(uid, tc.name) {
				t.Fatal("missing marker")
			}
			siteID := p.PrivateApp.SiteID(t, p.Olive, tc.name)
			host := tc.name + "." + p.Person
			for _, req := range []struct{ host, path string }{{host, "/"}, {p.Person, "/" + tc.name + "/"}, {servertest.PcContentHost, "/internal/site-redirect/" + p.Handle + "/" + tc.name + "/"}} {
				r := p.At(t, "GET", req.host, req.path, nil, nil)
				if strings.Contains(string(r.Body), servertest.PasscodeCanary) || (r.Status != 401 && r.Status/100 != 3) {
					t.Fatalf("anonymous %s%s: %d %s", req.host, req.path, r.Status, r.Body)
				}
			}
			// What the content host's disk server hands a marked site to:
			// never the content (named-viewer sites answer it without a page).
			if r := p.At(t, "GET", servertest.PcContentHost, "/internal/passcode/"+p.Handle+"/"+tc.name+"/", nil, nil); strings.Contains(string(r.Body), servertest.PasscodeCanary) || r.Status == 200 {
				t.Fatalf("content-host marker path: %d %s", r.Status, r.Body)
			}
			r = p.At(t, "GET", host, "/", nil, nil)
			if r.Status != 401 || !strings.Contains(r.Header.Get("X-Robots-Tag"), "noindex") {
				t.Fatalf("gate: %d %v", r.Status, r.Header)
			}
			for _, who := range []servertest.Person{p.Olive, mom} {
				ck := p.Session(t, who, siteID, host)
				if r := p.At(t, "GET", host, "/", nil, servertest.CookieOf(ck)); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
					t.Fatalf("allowed: %d %s", r.Status, r.Body)
				}
			}
			ck := p.Session(t, dad, siteID, host)
			if r := p.At(t, "GET", host, "/", nil, servertest.CookieOf(ck)); r.Status != 403 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
				t.Fatalf("unlisted: %d %s", r.Status, r.Body)
			}
			if r := p.At(t, "GET", servertest.PcSiteDomain, "/sitemap.xml", nil, nil); strings.Contains(string(r.Body), host) {
				t.Fatal("private site in sitemap")
			}
			if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/"+tc.name+"/storage/resources/menu", servertest.StorageRules("kv", servertest.RulesPublic), p.Okey); r.Status/100 != 2 {
				t.Fatalf("resource %d %s", r.Status, r.Body)
			}
			if r := p.At(t, "GET", host, "/v1/sites/"+tc.name+"/storage/kv/menu/keys/today", nil, servertest.Browser(host, "")); r.Status != 403 {
				t.Fatalf("storage gate %d %s", r.Status, r.Body)
			}
			// Existing specific: do not add the viewers supplied on a create-or-update.
			r = p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/"+tc.name+"/files?create=1", map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary}, "access": "specific", "viewers": []string{dad.Email}}, p.Okey)
			if r.Status != 200 {
				t.Fatalf("update specific %d %s", r.Status, r.Body)
			}
			if r := p.At(t, "GET", host, "/", nil, servertest.CookieOf(ck)); r.Status != 403 {
				t.Fatal("update added viewer")
			}
			r = p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/"+tc.name+"/files?create=1", map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary}, "access": "anyone"}, p.Okey)
			if r.Status != 200 || r.JSON(t)["access"] != "specific" {
				t.Fatalf("anyone changed access: %d %s", r.Status, r.Body)
			}
			// Preview links retain the existing bearer-link exception for named-viewer sites.
			preview := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/"+tc.name+"/versions/1/preview-link", nil, p.Okey)
			if preview.Status != 200 {
				t.Fatalf("preview link: %d %s", preview.Status, preview.Body)
			}
			u, err := url.Parse(preview.JSON(t)["url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			pr := p.At(t, "GET", u.Host, u.RequestURI(), nil, nil)
			if pr.Status != 200 || !strings.Contains(string(pr.Body), servertest.PasscodeCanary) || !strings.Contains(pr.Header.Get("X-Robots-Tag"), "noindex") {
				t.Fatalf("preview behavior: %d %s", pr.Status, pr.Body)
			}

		})
	}
}

func TestCreateAccessRefusalsAndDefault(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SiteViewersMax = 1 }, &p.Sites.Limits, &p.Access.Limits, &p.Data.Limits)
	uid, _ := p.UserID(t, p.Olive)
	for i, fields := range []map[string]any{{"access": "invalid"}, {"viewers": []string{"a@example.com"}}, {"access": "anyone", "viewers": []string{}}, {"access": "specific", "viewers": []string{"bad"}}, {"access": "specific", "viewers": []string{"a@example.com", "b@example.com"}}} {
		name := []string{"badaccess", "badviewers", "badanyone", "bademail", "toomany"}[i]
		fields["files"] = map[string]string{"index.html": servertest.PasscodeCanary}
		r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/"+name+"/files", fields, p.Okey)
		if r.Status != 400 {
			t.Fatalf("validation %d %s", r.Status, r.Body)
		}
		assertNoCreateAccessSite(t, p, uid, name)
	}
	body := map[string]any{"files": map[string]string{"index.html": "changed"}, "access": "specific"}
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/files?create=1", body, p.Okey); r.Status != 409 || r.JSON(t)["code"] != "access_differs" {
		t.Fatalf("differs %d %s", r.Status, r.Body)
	}
	if r := p.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/trip/files", body, p.Okey); r.Status != 400 {
		t.Fatalf("plain PUT %d %s", r.Status, r.Body)
	}
	if p.Sites.Disk.HasPasscodeMarker(uid, "trip") {
		t.Fatal("default marker")
	}
	if r := p.At(t, "GET", p.Host, "/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatal("default changed")
	}
	p.Sites.Hosts.SetPersonHosts("off")
	p.Sites.Hosts.SetSiteHosts("off", "")
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/sharedprivate/files", body, p.Okey); r.Status != 409 || r.JSON(t)["code"] != "named_viewers_need_own_address" {
		t.Fatalf("shared refusal %d %s", r.Status, r.Body)
	}
	assertNoCreateAccessSite(t, p, uid, "sharedprivate")
	p.Sites.Hosts.SetPersonHosts("canonical")
	p.Sites.Hosts.SetSiteHosts("canonical", "")
	p.Access.SetVisitorSignIn(false, nil)
	p.Sites.SetVisitorSignIn(false, nil)
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/refused/files", body, p.Okey); r.Status != 409 || r.JSON(t)["code"] != "sign_in_unavailable" {
		t.Fatalf("refusal %d %s", r.Status, r.Body)
	}
	assertNoCreateAccessSite(t, p, uid, "refused")
	p.Sites.Ed = servertest.GroupTestEdition()
	p.Sites.Hosts.SetEdition(p.Sites.Ed)
	p.Access.Ed, p.Data.Ed = p.Sites.Ed, p.Sites.Ed
	adminKey := map[string]string{"X-API-Key": p.Admin}
	admin := p.At(t, "GET", servertest.PcSiteDomain, "/v1/me", nil, adminKey).JSON(t)["id"].(string)
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/hackprivate/files", body, adminKey); r.Status != 409 || r.JSON(t)["code"] != "named_viewers_unavailable" {
		t.Fatalf("Hack refusal %d %s", r.Status, r.Body)
	}
	assertNoCreateAccessSite(t, p, admin, "hackprivate")
	body["access"] = "anyone"
	if r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/hackanyone/files", body, adminKey); r.Status != 201 {
		t.Fatalf("Hack anyone %d %s", r.Status, r.Body)
	}
	if p.Sites.Disk.HasPasscodeMarker(admin, "hackanyone") {
		t.Fatal("Hack anyone marker")
	}
}

func assertNoCreateAccessSite(t *testing.T, p *servertest.PasscodeApp, uid, name string) {
	t.Helper()
	var count int
	if err := p.Database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1 AND name=$2`, uid, name).Scan(&count); err != nil || count != 0 {
		t.Fatalf("row remains %s: %d %v", name, count, err)
	}
	if _, err := os.Stat(p.Sites.Disk.SiteDir(uid, name)); !os.IsNotExist(err) {
		t.Fatalf("site dir remains: %v", err)
	}
}

// Embed the real disk so only publication/marker failures differ.
type createAccessDisk struct {
	sites.Disk
	t                       *testing.T
	failMarker, failCurrent bool
	failAfterMarker         bool
	marked                  bool
}

func (d *createAccessDisk) SetPasscodeMarker(uid, name string, on bool) error {
	if on {
		if _, err := os.Stat(filepath.Join(d.SiteDir(uid, name), "current")); !os.IsNotExist(err) {
			d.t.Fatal("current existed before marker")
		}
		d.marked = true
		if d.failMarker {
			return nil
		} // Simulate the disk's missing-directory no-op.
	}
	err := d.Disk.SetPasscodeMarker(uid, name, on)
	if err == nil && on && d.failAfterMarker {
		return errors.New("injected failure after marker write")
	}
	return err
}
func (d *createAccessDisk) UpdateCurrent(uid, name string, v int) error {
	if !d.HasPasscodeMarker(uid, name) {
		d.t.Fatal("promotion without marker")
	}
	if d.failCurrent {
		return errors.New("injected promotion failure")
	}
	return d.Disk.UpdateCurrent(uid, name, v)
}
func TestCreateAccessDiskFailures(t *testing.T) {
	for _, failure := range []string{"missing-marker", "after-marker", "promotion"} {
		markerFailure := failure != "promotion"
		p := servertest.NewPasscodeApp(t)
		d := &createAccessDisk{Disk: p.Sites.Disk, t: t, failMarker: failure == "missing-marker", failAfterMarker: failure == "after-marker", failCurrent: failure == "promotion"}
		p.Sites.Disk = d
		p.Access.Disk, p.Data.Disk = p.Sites.Disk, p.Sites.Disk
		r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/failure/files", map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary}, "access": "specific"}, p.Okey)
		if r.Status != 500 || !d.marked {
			t.Fatalf("failure %d %s", r.Status, r.Body)
		}
		uid, _ := p.UserID(t, p.Olive)
		var count int
		_ = p.Database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1 AND name='failure'`, uid).Scan(&count)
		if markerFailure && (count != 0 || d.HasPasscodeMarker(uid, "failure")) {
			t.Fatal("failed create left row or marker")
		}
		if !markerFailure && (count != 1 || !d.HasPasscodeMarker(uid, "failure")) {
			t.Fatal("committed failure lost gate")
		}
		if r := p.At(t, "GET", "failure."+p.Person, "/", nil, nil); strings.Contains(string(r.Body), servertest.PasscodeCanary) {
			t.Fatal("failure opened content")
		}
	}
}

func TestCreateSpecificRace(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	// hammer requests name's addresses as an anonymous visitor until stop
	// closes, then for a further burst after the create returned. It reports
	// how many answers carried the content and how many were the sign-in page.
	hammer := func(name string, stop <-chan struct{}) (exposed, gated *atomic.Int64, wait func()) {
		exposed, gated = new(atomic.Int64), new(atomic.Int64)
		var wg sync.WaitGroup
		for w := 0; w < 2; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				after := -1
				for after != 0 {
					select {
					case <-stop:
						if after < 0 {
							after = 25
						}
					default:
					}
					if after > 0 {
						after--
					}
					for _, req := range []struct{ host, path string }{{name + "." + p.Person, "/"}, {p.Person, "/" + name + "/"}} {
						r := p.At(t, "GET", req.host, req.path, nil, nil)
						if strings.Contains(string(r.Body), servertest.PasscodeCanary) {
							exposed.Add(1)
						} else if r.Status == 401 {
							gated.Add(1)
						}
					}
				}
			}()
		}
		return exposed, gated, wg.Wait
	}
	for i := 0; i < 3; i++ {
		name := "racing" + strconv.Itoa(i)
		stop := make(chan struct{})
		exposed, gated, wait := hammer(name, stop)
		r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/"+name+"/files", map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary}, "access": "specific"}, p.Okey)
		close(stop)
		wait()
		if r.Status != 201 {
			t.Fatalf("create %d %s", r.Status, r.Body)
		}
		if exposed.Load() != 0 {
			t.Fatalf("%s: %d anonymous answers carried the content", name, exposed.Load())
		}
		if gated.Load() == 0 {
			t.Fatalf("%s: the hammer never reached the created site", name)
		}
	}
	// Control: the same hammer does see an open site's content, so a leak
	// above would have been caught.
	stop := make(chan struct{})
	exposed, _, wait := hammer("racingopen", stop)
	r := p.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/racingopen/files", map[string]any{"files": map[string]string{"index.html": servertest.PasscodeCanary}}, p.Okey)
	close(stop)
	wait()
	if r.Status != 201 || exposed.Load() == 0 {
		t.Fatalf("control: create %d, content seen %d times", r.Status, exposed.Load())
	}
}

func TestCreateSpecificMCP(t *testing.T) {
	p := servertest.NewPasscodeApp(t)
	token := p.Connect(t, p.Olive, p.RegisterClient(t, servertest.TestRedirect), servertest.TestRedirect)["access_token"].(string)
	r := p.Rpc(t, token, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{"site": "mcpprivate", "files": map[string]string{"index.html": servertest.PasscodeCanary}, "access": "specific"}})
	if !strings.Contains(string(r.Body), "Only you can open it") {
		t.Fatalf("MCP %s", r.Body)
	}
	if r := p.At(t, "GET", "mcpprivate."+p.Person, "/", nil, nil); r.Status != 401 || strings.Contains(string(r.Body), servertest.PasscodeCanary) {
		t.Fatalf("MCP first request %d %s", r.Status, r.Body)
	}
}
