package handler

import (
	"archive/zip"
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackWebsiteZipUploads(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant, outsider := a.newPerson(t, "zip-org"), a.newPerson(t, "zip-member"), a.newPerson(t, "zip-outsider")
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	a.startTeam(t, slug, "Zip team", participant)
	markReady(t, a.certDir, slug)
	upload := func(path string, files map[string]string, key string) resp {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		for name, text := range files {
			w, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(text)); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		return a.api(t, "PUT", path+"?create=1", b.String(), key)
	}
	base := "/v1/hack/events/" + slug
	files := map[string]string{"index.html": "hello"}
	wantTS(t, "own team", upload(base+"/site", files, participant.key), 201, "")
	wantTS(t, "update team", upload(base+"/site", files, participant.key), 200, "")
	wantTS(t, "outsider", upload(base+"/site", files, outsider.key), 404, "event_not_found")
	for _, tc := range []struct {
		files   map[string]string
		message string
		code    string
	}{
		{map[string]string{"nested/index.html": "hi"}, "no index.html at the root", "missing_index"},
		{map[string]string{"index.html": "hi", "server.py": "print('hi')"}, "disallowed file extension", "blocked_file_type"},
	} {
		r := upload(base+"/site", tc.files, participant.key)
		wantTS(t, "invalid archive", r, 400, tc.code)
		if !strings.Contains(string(r.body), tc.message) {
			t.Fatalf("missing reason: %s", r.body)
		}
	}
	wantTS(t, "bad zip", a.api(t, "PUT", base+"/site?create=1", "PKbroken", participant.key), 400, "invalid_archive")
	wantTS(t, "close", a.api(t, "POST", base+"/stage", map[string]string{"stage": "closed"}, org.key), 200, "")
	wantTS(t, "closed team", upload(base+"/site", files, participant.key), 409, "submissions_closed")
	wantTS(t, "closed before unpacking", a.api(t, "PUT", base+"/site?create=1", "PKbroken", participant.key), 409, "submissions_closed")
	draft := a.makeEvent(t, org)
	if _, err := a.database.Exec(`UPDATE events SET stage='draft' WHERE slug=$1`, draft); err != nil {
		t.Fatal(err)
	}
	path := "/v1/hack/events/" + draft + "/website"
	wantTS(t, "draft event", upload(path, files, org.key), 201, "")
	state := a.api(t, "GET", path, nil, org.key).json(t)
	if len(state["versions"].([]any)) != 1 || state["upload_limit_bytes"].(float64) <= 0 {
		t.Fatalf("missing versions or limits: %v", state)
	}
	wantTS(t, "event outsider", upload(path, files, participant.key), 404, "event_not_found")
}

func TestHackArchiveValidationReasons(t *testing.T) {
	old := hackMode
	SetHackMode(true)
	t.Cleanup(func() { SetHackMode(old) })
	h := &SiteHandler{}
	for _, tc := range []struct{ name, message string }{
		{"nested/index.html", "no index.html at the root"},
		{"index.html", ""},
	} {
		var body bytes.Buffer
		z := zip.NewWriter(&body)
		w, err := z.Create(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("PUT", "/", &body)
		rec := httptest.NewRecorder()
		_, _, err = h.readAndValidateFiles(rec, r, "team")
		if tc.message == "" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil || !strings.Contains(rec.Body.String(), tc.message) {
			t.Fatalf("missing reason: %s", rec.Body.String())
		}
	}
	r := httptest.NewRequest("PUT", "/", strings.NewReader("PK\x03\x04broken"))
	rec := httptest.NewRecorder()
	if _, _, err := h.readAndValidateFiles(rec, r, "team"); err == nil || !strings.Contains(rec.Body.String(), "open zip archive") {
		t.Fatalf("missing raw zip error: %s", rec.Body.String())
	}
}
