package export_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"regexp"
	"strings"
	"testing"

	"simplehost/core/testkit"
	datasql "simplehost/data/sql"
	"simplehost/ops/export"
)

// SPEC test 4 (F3): a queued download arrives as a link and holds sites,
// data and account details.
func TestDownloadMyData(t *testing.T) {
	k := testkit.Start(t, export.Mount, datasql.Mount)
	nina := k.Person(t, "nina")
	k.Publish(t, nina, "blog", map[string]string{"index.html": "hello", "posts/one.html": "one"})
	b := k.Browser()
	auth := []string{"Authorization", "Bearer " + nina.Key}
	testkit.Call(t, b, "POST", "https://simple-host.app/v1/sites/blog/storage/sqlite/notes/execute", map[string]string{"sql": "CREATE TABLE notes (body TEXT)"}, auth...)

	if r := testkit.Call(t, b, "POST", "https://simple-host.app/v1/me/export", map[string]any{}, auth...); r.Status != 202 {
		t.Fatalf("queue: %d %s", r.Status, r.Body)
	}
	if made, err := export.Next(context.Background(), k.App); !made || err != nil {
		t.Fatalf("made %v: %v", made, err)
	}
	mails := k.Mail.To(nina.Email)
	link := regexp.MustCompile(`https://simple-host\.app/v1/exports/\S+`).FindString(mails[len(mails)-1].Text)
	if link == "" {
		t.Fatalf("no link in %q", mails[len(mails)-1].Text)
	}
	r := testkit.Call(t, k.Browser(), "GET", link, nil)
	z, err := zip.NewReader(bytes.NewReader([]byte(r.Body)), int64(len(r.Body)))
	if r.Status != 200 || err != nil {
		t.Fatalf("download: %d %v", r.Status, err)
	}
	got := map[string]string{}
	for _, f := range z.File {
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		got[f.Name] = string(body)
	}
	if !strings.Contains(got["account.json"], nina.Email) || got["sites/nina/blog/files/posts/one.html"] != "one" ||
		!strings.HasPrefix(got["sites/nina/blog/data/notes.sqlite"], "SQLite format 3") {
		t.Fatalf("download holds %v", func() (n []string) {
			for k := range got {
				n = append(n, k)
			}
			return
		}())
	}
	if r := testkit.Call(t, k.Browser(), "GET", "https://simple-host.app/v1/exports/ex_wrong", nil); r.Status != 404 {
		t.Fatalf("a wrong link: %d", r.Status)
	}
}
