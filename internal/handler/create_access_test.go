package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
)

func TestCreateAccessValidation(t *testing.T) {
	h := &SiteHandler{}
	h.limits = config.DefaultLimits()
	h.limits.SiteViewersMax = 1
	for _, tc := range []struct {
		access  string
		viewers []string
		code    string
	}{
		{"bogus", nil, "invalid_request"}, {"anyone", []string{}, "invalid_request"},
		{"specific", []string{"bad"}, "invalid_email"}, {"specific", []string{"a@example.com", "b@example.com"}, "too_many_viewers"},
	} {
		r := httptest.NewRequest("POST", "/v1/sites/s/files", nil)
		w := httptest.NewRecorder()
		if h.readCreateAccess(w, r, &tc.access, tc.viewers) || w.Code != 400 || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body)
		}
	}
	access := "specific"
	r := httptest.NewRequest("POST", "/v1/sites/s/files", nil)
	if !h.readCreateAccess(httptest.NewRecorder(), r, &access, []string{" A@Example.com ", "a@example.com"}) || len(creationAccess(r).Viewers) != 1 {
		t.Fatal("normalization/dedupe")
	}
}

func TestCreateAccessArchiveOptions(t *testing.T) {
	h := &SiteHandler{}
	h.limits = config.DefaultLimits()
	for _, path := range []string{
		"/v1/sites/s?access=specific&viewers=A@example.com,b@example.com&viewers=a@example.com",
		"/v1/sites/s?access=specific&viewers=A@example.com&viewers=b@example.com",
	} {
		r := httptest.NewRequest("POST", path, nil)
		if !h.readArchiveCreateAccess(httptest.NewRecorder(), r) || len(creationAccess(r).Viewers) != 2 {
			t.Fatal("archive viewer parsing")
		}
	}
	r := httptest.NewRequest("PUT", "/v1/sites/s?access=specific", nil)
	w := httptest.NewRecorder()
	if h.readArchiveCreateAccess(w, r) || w.Code != 400 {
		t.Fatal("plain PUT accepted access")
	}
	// A query that names access but does not parse never falls back to open.
	for _, path := range []string{"/v1/sites/s?access=specific;viewers=a@example.com", "/v1/sites/s?access=specific%ZZ", "/v1/sites/s?viewers=a@example.com;x", "/v1/sites/s?%61ccess=specific%ZZ", "/v1/sites/s?x=%ZZ&%61ccess=specific;y", "/v1/sites/s?%ZZ=1;a=b"} {
		r := &http.Request{Method: "POST", URL: &url.URL{Path: "/v1/sites/s", RawQuery: strings.SplitN(path, "?", 2)[1]}}
		w := httptest.NewRecorder()
		if h.readArchiveCreateAccess(w, r) || w.Code != 400 {
			t.Fatalf("malformed query %s accepted: %d", path, w.Code)
		}
	}
	// A malformed query that does not mention the options is left alone.
	if r := (&http.Request{Method: "POST", URL: &url.URL{Path: "/v1/sites/s", RawQuery: "x=1;y=2"}}); !h.readArchiveCreateAccess(httptest.NewRecorder(), r) || creationAccess(r).Access != "" {
		t.Fatal("unrelated malformed query refused")
	}
	// Field names match without regard to case, null included.
	for _, data := range []string{`{"Access":null}`, `{"ACCESS":null}`, `{"Viewers":null}`} {
		var body filesRequest
		if err := json.Unmarshal([]byte(data), &body); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/v1/sites/s/files", nil)
		if h.readCreateAccess(httptest.NewRecorder(), r, body.Access, body.Viewers) {
			t.Fatalf("%s treated as omitted", data)
		}
	}
	var upper filesRequest
	if err := json.Unmarshal([]byte(`{"Access":"specific","VIEWERS":["a@example.com"]}`), &upper); err != nil || upper.Access == nil || *upper.Access != "specific" || len(upper.Viewers) != 1 {
		t.Fatalf("case-insensitive fields: %v %+v", err, upper)
	}
	for _, data := range []string{`{"access":null}`, `{"viewers":null}`, `{"access":"anyone","viewers":[]}`} {
		var body filesRequest
		if err := json.Unmarshal([]byte(data), &body); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("PUT", "/v1/sites/s/files", nil)
		if h.readCreateAccess(httptest.NewRecorder(), r, body.Access, body.Viewers) {
			t.Fatalf("plain PUT ignored %s", data)
		}
	}
}

func TestCreateSpecificFirstRequest(t *testing.T) {
	p := newPasscodeApp(t)
	mom, dad := p.newPerson(t, "mom"), p.newPerson(t, "dad")
	for _, tc := range []struct {
		name, method, suffix string
		archive              bool
	}{
		{"jsonprivate", "POST", "/files", false}, {"putprivate", "PUT", "/files?create=1", false},
		{"zipprivate", "POST", "?access=specific&viewers=" + mom.email, true}, {"putzipprivate", "PUT", "?create=1&access=specific&viewers=" + mom.email, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body any = map[string]any{"files": map[string]string{"index.html": passcodeCanary}, "access": "specific", "viewers": []string{mom.email}}
			if tc.archive {
				var b bytes.Buffer
				z := zip.NewWriter(&b)
				f, _ := z.Create("index.html")
				_, _ = f.Write([]byte(passcodeCanary))
				_ = z.Close()
				body = b.String()
			}
			r := p.at(t, tc.method, pcSiteDomain, "/v1/sites/"+tc.name+tc.suffix, body, p.okey)
			if r.status != 201 || r.json(t)["access"] != "specific" {
				t.Fatalf("create: %d %s", r.status, r.body)
			}
			uid, _ := p.userID(t, p.olive)
			if !p.sites.disk.HasPasscodeMarker(uid, tc.name) {
				t.Fatal("missing marker")
			}
			siteID := p.privateApp.siteID(t, p.olive, tc.name)
			host := tc.name + "." + p.person
			for _, req := range []struct{ host, path string }{{host, "/"}, {p.person, "/" + tc.name + "/"}, {pcContentHost, "/internal/site-redirect/" + p.handle + "/" + tc.name + "/"}} {
				r := p.at(t, "GET", req.host, req.path, nil, nil)
				if strings.Contains(string(r.body), passcodeCanary) || (r.status != 401 && r.status/100 != 3) {
					t.Fatalf("anonymous %s%s: %d %s", req.host, req.path, r.status, r.body)
				}
			}
			// What the content host's disk server hands a marked site to:
			// never the content (named-viewer sites answer it without a page).
			if r := p.at(t, "GET", pcContentHost, "/internal/passcode/"+p.handle+"/"+tc.name+"/", nil, nil); strings.Contains(string(r.body), passcodeCanary) || r.status == 200 {
				t.Fatalf("content-host marker path: %d %s", r.status, r.body)
			}
			r = p.at(t, "GET", host, "/", nil, nil)
			if r.status != 401 || !strings.Contains(r.header.Get("X-Robots-Tag"), "noindex") {
				t.Fatalf("gate: %d %v", r.status, r.header)
			}
			for _, who := range []person{p.olive, mom} {
				ck := p.session(t, who, siteID, host)
				if r := p.at(t, "GET", host, "/", nil, cookieOf(ck)); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
					t.Fatalf("allowed: %d %s", r.status, r.body)
				}
			}
			ck := p.session(t, dad, siteID, host)
			if r := p.at(t, "GET", host, "/", nil, cookieOf(ck)); r.status != 403 || strings.Contains(string(r.body), passcodeCanary) {
				t.Fatalf("unlisted: %d %s", r.status, r.body)
			}
			if r := p.at(t, "GET", pcSiteDomain, "/sitemap.xml", nil, nil); strings.Contains(string(r.body), host) {
				t.Fatal("private site in sitemap")
			}
			if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/"+tc.name+"/storage/resources/menu", map[string]any{"kind": "kv", "read": "anyone", "write": "owner"}, p.okey); r.status/100 != 2 {
				t.Fatalf("resource %d %s", r.status, r.body)
			}
			if r := p.at(t, "GET", host, "/v1/sites/"+tc.name+"/storage/kv/menu/keys/today", nil, browser(host, "")); r.status != 403 {
				t.Fatalf("storage gate %d %s", r.status, r.body)
			}
			// Existing specific: do not add the viewers supplied on a create-or-update.
			r = p.at(t, "PUT", pcSiteDomain, "/v1/sites/"+tc.name+"/files?create=1", map[string]any{"files": map[string]string{"index.html": passcodeCanary}, "access": "specific", "viewers": []string{dad.email}}, p.okey)
			if r.status != 200 {
				t.Fatalf("update specific %d %s", r.status, r.body)
			}
			if r := p.at(t, "GET", host, "/", nil, cookieOf(ck)); r.status != 403 {
				t.Fatal("update added viewer")
			}
			r = p.at(t, "PUT", pcSiteDomain, "/v1/sites/"+tc.name+"/files?create=1", map[string]any{"files": map[string]string{"index.html": passcodeCanary}, "access": "anyone"}, p.okey)
			if r.status != 200 || r.json(t)["access"] != "specific" {
				t.Fatalf("anyone changed access: %d %s", r.status, r.body)
			}
			// Preview links retain the existing bearer-link exception for named-viewer sites.
			preview := p.at(t, "POST", pcSiteDomain, "/v1/sites/"+tc.name+"/versions/1/preview-link", nil, p.okey)
			if preview.status != 200 {
				t.Fatalf("preview link: %d %s", preview.status, preview.body)
			}
			u, err := url.Parse(preview.json(t)["url"].(string))
			if err != nil {
				t.Fatal(err)
			}
			pr := p.at(t, "GET", u.Host, u.RequestURI(), nil, nil)
			if pr.status != 200 || !strings.Contains(string(pr.body), passcodeCanary) || !strings.Contains(pr.header.Get("X-Robots-Tag"), "noindex") {
				t.Fatalf("preview behavior: %d %s", pr.status, pr.body)
			}

		})
	}
}

func TestCreateAccessRefusalsAndDefault(t *testing.T) {
	p := newPasscodeApp(t)
	withPasscodeLimits(t, func(l *config.Limits) { l.SiteViewersMax = 1 }, &p.sites.limitSet)
	uid, _ := p.userID(t, p.olive)
	for i, fields := range []map[string]any{{"access": "invalid"}, {"viewers": []string{"a@example.com"}}, {"access": "anyone", "viewers": []string{}}, {"access": "specific", "viewers": []string{"bad"}}, {"access": "specific", "viewers": []string{"a@example.com", "b@example.com"}}} {
		name := []string{"badaccess", "badviewers", "badanyone", "bademail", "toomany"}[i]
		fields["files"] = map[string]string{"index.html": passcodeCanary}
		r := p.at(t, "POST", pcSiteDomain, "/v1/sites/"+name+"/files", fields, p.okey)
		if r.status != 400 {
			t.Fatalf("validation %d %s", r.status, r.body)
		}
		assertNoCreateAccessSite(t, p, uid, name)
	}
	body := map[string]any{"files": map[string]string{"index.html": "changed"}, "access": "specific"}
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/files?create=1", body, p.okey); r.status != 409 || r.json(t)["code"] != "access_differs" {
		t.Fatalf("differs %d %s", r.status, r.body)
	}
	if r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/files", body, p.okey); r.status != 400 {
		t.Fatalf("plain PUT %d %s", r.status, r.body)
	}
	if p.sites.disk.HasPasscodeMarker(uid, "trip") {
		t.Fatal("default marker")
	}
	if r := p.at(t, "GET", p.host, "/", nil, nil); r.status != 200 || !strings.Contains(string(r.body), passcodeCanary) {
		t.Fatal("default changed")
	}
	p.sites.SetPersonHosts("off")
	p.sites.SetSiteHosts("off", "")
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/sharedprivate/files", body, p.okey); r.status != 409 || r.json(t)["code"] != "named_viewers_need_own_address" {
		t.Fatalf("shared refusal %d %s", r.status, r.body)
	}
	assertNoCreateAccessSite(t, p, uid, "sharedprivate")
	p.sites.SetPersonHosts("canonical")
	p.sites.SetSiteHosts("canonical", "")
	p.sites.SetVisitorSignIn(false, nil)
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/refused/files", body, p.okey); r.status != 409 || r.json(t)["code"] != "sign_in_unavailable" {
		t.Fatalf("refusal %d %s", r.status, r.body)
	}
	assertNoCreateAccessSite(t, p, uid, "refused")
	p.sites.ed = edition.ForKind(edition.Hack)
	adminKey := map[string]string{"X-API-Key": p.admin}
	admin := p.at(t, "GET", pcSiteDomain, "/v1/me", nil, adminKey).json(t)["id"].(string)
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/hackprivate/files", body, adminKey); r.status != 409 || r.json(t)["code"] != "named_viewers_unavailable" {
		t.Fatalf("Hack refusal %d %s", r.status, r.body)
	}
	assertNoCreateAccessSite(t, p, admin, "hackprivate")
	body["access"] = "anyone"
	if r := p.at(t, "POST", pcSiteDomain, "/v1/sites/hackanyone/files", body, adminKey); r.status != 201 {
		t.Fatalf("Hack anyone %d %s", r.status, r.body)
	}
	if p.sites.disk.HasPasscodeMarker(admin, "hackanyone") {
		t.Fatal("Hack anyone marker")
	}
}

func assertNoCreateAccessSite(t *testing.T, p *passcodeApp, uid, name string) {
	t.Helper()
	var count int
	if err := p.database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1 AND name=$2`, uid, name).Scan(&count); err != nil || count != 0 {
		t.Fatalf("row remains %s: %d %v", name, count, err)
	}
	if _, err := os.Stat(p.sites.disk.SiteDir(uid, name)); !os.IsNotExist(err) {
		t.Fatalf("site dir remains: %v", err)
	}
}

// Embed the real disk so only publication/marker failures differ.
type createAccessDisk struct {
	SiteDisk
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
	err := d.SiteDisk.SetPasscodeMarker(uid, name, on)
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
	return d.SiteDisk.UpdateCurrent(uid, name, v)
}
func TestCreateAccessDiskFailures(t *testing.T) {
	for _, failure := range []string{"missing-marker", "after-marker", "promotion"} {
		markerFailure := failure != "promotion"
		p := newPasscodeApp(t)
		d := &createAccessDisk{SiteDisk: p.sites.disk, t: t, failMarker: failure == "missing-marker", failAfterMarker: failure == "after-marker", failCurrent: failure == "promotion"}
		p.sites.disk = d
		r := p.at(t, "POST", pcSiteDomain, "/v1/sites/failure/files", map[string]any{"files": map[string]string{"index.html": passcodeCanary}, "access": "specific"}, p.okey)
		if r.status != 500 || !d.marked {
			t.Fatalf("failure %d %s", r.status, r.body)
		}
		uid, _ := p.userID(t, p.olive)
		var count int
		_ = p.database.QueryRow(`SELECT count(*) FROM sites WHERE user_id=$1 AND name='failure'`, uid).Scan(&count)
		if markerFailure && (count != 0 || d.HasPasscodeMarker(uid, "failure")) {
			t.Fatal("failed create left row or marker")
		}
		if !markerFailure && (count != 1 || !d.HasPasscodeMarker(uid, "failure")) {
			t.Fatal("committed failure lost gate")
		}
		if r := p.at(t, "GET", "failure."+p.person, "/", nil, nil); strings.Contains(string(r.body), passcodeCanary) {
			t.Fatal("failure opened content")
		}
	}
}

func TestCreateSpecificRace(t *testing.T) {
	p := newPasscodeApp(t)
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
					for _, req := range []struct{ host, path string }{{name + "." + p.person, "/"}, {p.person, "/" + name + "/"}} {
						r := p.at(t, "GET", req.host, req.path, nil, nil)
						if strings.Contains(string(r.body), passcodeCanary) {
							exposed.Add(1)
						} else if r.status == 401 {
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
		r := p.at(t, "POST", pcSiteDomain, "/v1/sites/"+name+"/files", map[string]any{"files": map[string]string{"index.html": passcodeCanary}, "access": "specific"}, p.okey)
		close(stop)
		wait()
		if r.status != 201 {
			t.Fatalf("create %d %s", r.status, r.body)
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
	r := p.at(t, "POST", pcSiteDomain, "/v1/sites/racingopen/files", map[string]any{"files": map[string]string{"index.html": passcodeCanary}}, p.okey)
	close(stop)
	wait()
	if r.status != 201 || exposed.Load() == 0 {
		t.Fatalf("control: create %d, content seen %d times", r.status, exposed.Load())
	}
}

func TestCreateSpecificMCP(t *testing.T) {
	p := newPasscodeApp(t)
	token := p.connect(t, p.olive, p.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	r := p.rpc(t, token, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{"site": "mcpprivate", "files": map[string]string{"index.html": passcodeCanary}, "access": "specific"}})
	if !strings.Contains(string(r.body), "Only you can open it") {
		t.Fatalf("MCP %s", r.body)
	}
	if r := p.at(t, "GET", "mcpprivate."+p.person, "/", nil, nil); r.status != 401 || strings.Contains(string(r.body), passcodeCanary) {
		t.Fatalf("MCP first request %d %s", r.status, r.body)
	}
}
