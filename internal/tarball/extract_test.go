package tarball

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// makeTarGz builds a .tar.gz from the given entries. A nil content with a
// non-zero typeflag lets tests emit special (non-regular) entries.
func makeTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typeflag
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.content)),
			Typeflag: typ,
			Linkname: e.linkname,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if len(e.content) > 0 {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatalf("write content %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	content  []byte
	typeflag byte
	linkname string
}

func TestExtractHappyPath(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "index.html", content: []byte("<h1>hi</h1>")},
		{name: "css/app.css", content: []byte("body{}")},
		{name: "./about.html", content: []byte("about")}, // leading ./ normalized
	})
	files, err := Extract(bytes.NewReader(data), "site.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"index.html", "css/app.css", "about.html"} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing expected file %q (got %v)", want, keys(files))
		}
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files, got %d: %v", len(files), keys(files))
	}
}

func TestExtractRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../escape.txt", "../../etc/passwd", "a/../../b.txt"} {
		data := makeTarGz(t, []tarEntry{{name: name, content: []byte("x")}})
		if _, err := Extract(bytes.NewReader(data), "site.tar.gz"); err == nil {
			t.Errorf("expected traversal %q to be rejected", name)
		}
	}
}

func TestExtractSkipsSecrets(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "index.html", content: []byte("ok")},
		{name: ".env", content: []byte("SECRET=1")},
		{name: ".git/config", content: []byte("[core]")},
		{name: "sub/.htpasswd", content: []byte("user:hash")},
		{name: ".env.example", content: []byte("SECRET=")}, // example is allowed
	})
	files, err := Extract(bytes.NewReader(data), "site.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, secret := range []string{".env", ".git/config", "sub/.htpasswd"} {
		if _, ok := files[secret]; ok {
			t.Errorf("secret %q should have been skipped", secret)
		}
	}
	if _, ok := files[".env.example"]; !ok {
		t.Errorf(".env.example should be kept")
	}
}

func TestExtractSkipsNonRegularEntries(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "index.html", content: []byte("ok")},
		{name: "evil-link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		{name: "dev-node", typeflag: tar.TypeChar},
	})
	files, err := Extract(bytes.NewReader(data), "site.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := files["evil-link"]; ok {
		t.Errorf("symlink entry must not be extracted")
	}
	if _, ok := files["dev-node"]; ok {
		t.Errorf("device entry must not be extracted")
	}
	if len(files) != 1 {
		t.Errorf("expected only the regular file, got %v", keys(files))
	}
}

func TestExtractRejectsDuplicates(t *testing.T) {
	data := makeTarGz(t, []tarEntry{
		{name: "page.html", content: []byte("first")},
		{name: "page.html", content: []byte("second")},
	})
	if _, err := Extract(bytes.NewReader(data), "site.tar.gz"); err == nil {
		t.Error("expected duplicate entry to be rejected")
	}
}

func TestExtractRejectsOversizeFile(t *testing.T) {
	big := bytes.Repeat([]byte("A"), maxFileSize+1)
	data := makeTarGz(t, []tarEntry{{name: "big.bin", content: big}})
	_, err := Extract(bytes.NewReader(data), "site.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Errorf("expected size-limit rejection, got %v", err)
	}
}

func TestExtractRejectsTooManyEntries(t *testing.T) {
	entries := make([]tarEntry, maxEntryCount+1)
	for i := range entries {
		entries[i] = tarEntry{name: "f" + itoa(i) + ".txt", content: []byte("x")}
	}
	data := makeTarGz(t, entries)
	_, err := Extract(bytes.NewReader(data), "site.tar.gz")
	if err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Errorf("expected entry-limit rejection, got %v", err)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
