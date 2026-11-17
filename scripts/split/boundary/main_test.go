package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestNamesBoundary(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "scripts/allow/source-names.txt", "# no exceptions\n")
	writeFixture(t, root, "host/app/plain.go", "package app\n// HackComment\nvar Plain = `HackString`\n")
	writeFixture(t, root, "products/hack/product.go", "package hack\nvar HackProduct = 1\n")
	if err := run(root, "names", ""); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"host/app/bad.go", "host/app/plain_test.go", "host/app/testdata/data.go", "host/db/migrations/hack.sql"} {
		t.Run(path, func(t *testing.T) {
			writeFixture(t, root, path, "package app\nvar HackThing = 1\n")
			if err := run(root, "names", ""); err == nil {
				t.Fatal("accepted product name")
			}
			os.Remove(filepath.Join(root, path))
		})
	}
}

func TestPublicTextAndArchives(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"page.txt", "Simple Hack"}, {"page.txt", "hAcKaThOn"},
		{"simple-hack.txt", "ordinary copy"}, {"simple-hack/page.txt", "ordinary copy"},
		{"simple-hack/", ""},
	} {
		for _, archive := range []string{"plain", "zip", "gzip", "zip-gzip", "tar"} {
			t.Run(tc.name+"/"+tc.body+"/"+archive, func(t *testing.T) {
				path, data := tc.name, []byte(tc.body)
				if strings.Contains(archive, "gzip") {
					var b bytes.Buffer
					g := gzip.NewWriter(&b)
					g.Name = path
					g.Write(data)
					g.Close()
					path, data = "neutral.gz", b.Bytes()
				}
				if strings.Contains(archive, "zip") {
					var b bytes.Buffer
					z := zip.NewWriter(&b)
					w, err := z.Create(path)
					if err != nil {
						t.Fatal(err)
					}
					w.Write(data)
					z.Close()
					path, data = "neutral.zip", b.Bytes()
				}
				if archive == "tar" {
					var b bytes.Buffer
					w := tar.NewWriter(&b)
					h := &tar.Header{Name: path, Mode: 0644, Size: int64(len(data))}
					if strings.HasSuffix(path, "/") {
						h.Typeflag = tar.TypeDir
					}
					if err := w.WriteHeader(h); err != nil {
						t.Fatal(err)
					}
					if _, err := w.Write(data); err != nil {
						t.Fatal(err)
					}
					if err := w.Close(); err != nil {
						t.Fatal(err)
					}
					path, data = "neutral.tar", b.Bytes()
				}
				rows, err := assetFindings("host/webassets/"+path, data)
				if err != nil {
					t.Fatal(err)
				}
				root := t.TempDir()
				allow := filepath.Join(root, "allow.txt")
				writeFixture(t, root, "allow.txt", "# no exceptions\n")
				if err := checkCounts(allow, "host/", rows); err == nil {
					t.Fatal("accepted embedded product name")
				}
			})
		}
	}
}

func TestExactAllowCounts(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "allow.txt")
	writeFixture(t, root, "allow.txt", "host/page.html | 1 | hosted cross-reference to the sister product\n")
	rows := textFindings("host/page.html", []byte("Simple Hack"))
	if err := checkCounts(path, "host/", rows); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]finding{nil, append(rows, rows...), {{path: "host/other.html", kind: "text", value: "Simple Hack"}}} {
		if err := checkCounts(path, "host/", bad); err == nil {
			t.Fatal("accepted stale or extra match")
		}
	}
	writeFixture(t, root, "allow.txt", "host/page.html | 1 | \n")
	if err := checkCounts(path, "host/", rows); err == nil {
		t.Fatal("accepted missing reason")
	}
}

func TestPublicTextInputsAndAllEmbeds(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/boundaryfixture\n\ngo 1.24\n")
	for _, pkg := range []string{"webassets", "mcp", "db", "app/storage", "storage/rules"} {
		writeFixture(t, root, "host/"+pkg+"/embed.go", "package fixture\nimport \"embed\"\n//go:embed all:assets\nvar files embed.FS\n")
		writeFixture(t, root, "host/"+pkg+"/assets/.hidden.txt", "Simple Hack")
		writeFixture(t, root, "host/"+pkg+"/unembedded.txt", "Simple Hack")
	}
	writeFixture(t, root, "host/storage/rules/go.mod", "module example.com/enginefixture\n\ngo 1.24\n")
	writeFixture(t, root, "recording/text/host/page.txt", "Simple Hack")
	writeFixture(t, root, "recording/text/hack/page.txt", "Simple Hack")
	writeFixture(t, root, "scripts/allow/public-text.txt", "# no exceptions\n")
	recorded, assets, err := publicText(root, filepath.Join(root, "recording/text/host"))
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || len(assets) != 5 {
		t.Fatalf("recorded=%v assets=%v", recorded, assets)
	}
	if err := run(root, "public-text", filepath.Join(root, "recording/text/host")); err == nil {
		t.Fatal("accepted product text")
	}
	for _, dir := range []string{filepath.Join(root, "missing"), t.TempDir()} {
		if _, _, err := publicText(root, dir); err == nil {
			t.Fatal("accepted empty/missing recording")
		}
	}
}

func TestHostProgramRejectsProductDependency(t *testing.T) {
	script, err := filepath.Abs("../../check-binary.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module github.com/vsriram/simple-host\n\ngo 1.24\n")
	writeFixture(t, root, "products/hack/hack.go", "package hack\n")
	writeFixture(t, root, "cmd/simple-host/main.go", "package main\nimport _ \"github.com/vsriram/simple-host/products/hack\"\nfunc main() {}\n")
	cmd := exec.Command("bash", script, "--root", root)
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "forbidden product package") {
		t.Fatalf("err=%v output=%s", err, out)
	}
}
