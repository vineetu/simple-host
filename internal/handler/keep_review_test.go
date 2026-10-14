package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/tarball"
)

func keepReviewSite(t *testing.T) (*connectorApp, *db.User) {
	t.Helper()
	a := newConnectorApp(t)
	person := a.newPerson(t, "keep-review")
	headers := map[string]string{"X-API-Key": person.key, "Content-Type": "application/json"}
	for _, text := range []string{"version one", "version two"} {
		method := "PUT"
		if text == "version one" {
			method = "POST"
		}
		res := a.do(t, method, "/v1/sites/blog/files", jsonBody(map[string]any{"files": map[string]string{"index.html": text, "about.html": text}}), headers)
		if res.status < 200 || res.status >= 300 {
			t.Fatal(res.status, string(res.body))
		}
	}
	user, err := db.GetUserByUsername(context.Background(), a.database, person.email)
	if err != nil {
		t.Fatal(err)
	}
	return a, &user
}
func keepReviewRequest(user *db.User, body io.Reader) *http.Request {
	r := httptest.NewRequest("PUT", "/v1/sites/blog/files?keep=1", body)
	r.SetPathValue("sitename", "blog")
	return r.WithContext(auth.WithUser(r.Context(), user))
}

type keepReadSignal struct {
	io.Reader
	done chan struct{}
	sent bool
}

func (r *keepReadSignal) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if !r.sent {
		close(r.done)
		r.sent = true
	}
	return n, err
}

func TestKeepSnapshotUnderSiteLock(t *testing.T) {
	a, user := keepReviewSite(t)
	// No quota lock: keep=1 still has to wait before reading the live version.
	l := *config.Active()
	l.MaxSiteTotalMB = 0
	l.MaxAccountMB = 0
	l.MaxSiteTotalOverrides = ""
	l.MaxAccountOverrides = ""
	old := *config.Active()
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })
	unlock := a.sites.lockSite(user.ID, "blog")
	released := false
	defer func() {
		if !released {
			unlock()
		}
	}()
	read := make(chan struct{})
	result := make(chan *httptest.ResponseRecorder)
	body := &keepReadSignal{Reader: strings.NewReader(`{"files":{"about.html":"patched"}}`), done: read}
	go func() {
		w := httptest.NewRecorder()
		a.sites.updateSiteFiles(w, keepReviewRequest(user, body))
		result <- w
	}()
	<-read
	select {
	case <-result:
		t.Fatal("commit bypassed site lock")
	case <-time.After(100 * time.Millisecond):
	}
	// Model a rollback while holding its site lock. The patch must use version 1.
	if _, err := a.database.Exec(`UPDATE sites SET active_version=1 WHERE user_id=$1 AND name='blog'`, user.ID); err != nil {
		t.Fatal(err)
	}
	unlock()
	released = true
	select {
	case w := <-result:
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("site mutex reacquired during commit")
	}
	site, err := db.GetSiteByUser(context.Background(), a.database, user.ID, "blog")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(a.sites.disk.VersionDir(user.ID, "blog", 3), "index.html"))
	if site.ActiveVersion != 3 || err != nil || string(content) != "version one" {
		t.Fatal("patch undid rollback", site.ActiveVersion, err, string(content))
	}
}
func TestKeepMergedUnpackedCaps(t *testing.T) {
	a, user := keepReviewSite(t)
	site, err := db.GetSiteByUser(context.Background(), a.database, user.ID, "blog")
	if err != nil {
		t.Fatal(err)
	}
	dir := a.sites.disk.VersionDir(user.ID, "blog", site.ActiveVersion)
	body := []byte(strings.Repeat("x", 2<<20))
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), body, 0600); err != nil {
		t.Fatal(err)
	}
	request := keepReviewRequest(user, nil)
	w := httptest.NewRecorder()
	// A 1 MB request budget must not replace the ordinary 500 MB unpacked budget.
	files, _, err := a.sites.finishJSONFiles(w, request, "blog", map[string][]byte{"about.html": []byte("patch")}, nil, 1<<20, false, true)
	if err != nil || len(files["large.txt"]) != len(body) {
		t.Fatal("request cap applied to retained bytes", err)
	}
	// An explicit account budget must still constrain the merged result.
	_, _, err = a.sites.finishJSONFiles(httptest.NewRecorder(), request, "blog", map[string][]byte{"about.html": []byte("patch")}, nil, 1<<20, true, true)
	if !errors.Is(err, tarball.ErrTooLarge) {
		t.Fatal("override ignored", err)
	}
}
func TestKeepDecodesAndValidatesBeforeLocks(t *testing.T) {
	a, user := keepReviewSite(t)
	old := *config.Active()
	l := old
	l.MaxAccountMB = 1024
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })
	read := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	body := &keepStalledBody{read: read, release: release}
	go func() {
		defer close(done)
		a.sites.updateSiteFiles(httptest.NewRecorder(), keepReviewRequest(user, body))
	}()
	<-read
	lockResult := make(chan func())
	go func() { lockResult <- a.sites.lockAccountFiles(user.ID) }()
	select {
	case unlock := <-lockResult:
		unlock()
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("stalled decode held account lock")
	}
	// Invalid patch validation should finish even while both deploy locks are held.
	unlockAccount := a.sites.lockAccountFiles(user.ID)
	unlockSite := a.sites.lockSite(user.ID, "blog")
	close(release)
	<-done
	invalid := make(chan int)
	go func() {
		w := httptest.NewRecorder()
		a.sites.updateSiteFiles(w, keepReviewRequest(user, strings.NewReader(`{"files":{"../bad.html":"bad"}}`)))
		invalid <- w.Code
	}()
	select {
	case code := <-invalid:
		if code != 400 {
			t.Error(code)
		}
	case <-time.After(time.Second):
		t.Error("validation waited for deploy locks")
	}
	unlockSite()
	unlockAccount()
}

type keepStalledBody struct {
	read, release chan struct{}
	started       bool
}

func (r *keepStalledBody) Read(p []byte) (int, error) {
	if !r.started {
		close(r.read)
		r.started = true
	}
	<-r.release
	return 0, io.EOF
}
