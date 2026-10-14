package handler

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/wpimport"
)

type importArchiveEntry struct {
	name, body string
	kind       byte
}

func makeImportArchive(t *testing.T, format string, entries []importArchiveEntry) []byte {
	t.Helper()
	var b bytes.Buffer
	if format == "zip" {
		zw := zip.NewWriter(&b)
		for _, e := range entries {
			header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
			if e.kind == tar.TypeSymlink {
				header.SetMode(os.ModeSymlink | 0777)
			}
			w, err := zw.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			io.WriteString(w, e.body)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	var dst io.Writer = &b
	var gz *gzip.Writer
	if format == "tgz" {
		gz = gzip.NewWriter(&b)
		dst = gz
	}
	tw := tar.NewWriter(dst)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(e.body))
		if kind != tar.TypeReg {
			size = 0
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0644, Size: size, Typeflag: kind, Linkname: "target", Format: tar.FormatUSTAR}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			io.WriteString(tw, e.body)
		}
	}
	tw.Close()
	if gz != nil {
		gz.Close()
	}
	return b.Bytes()
}
func unpackImportTest(t *testing.T, body []byte, l config.Limits) (*importFiles, error) {
	t.Helper()
	dir := t.TempDir()
	name := filepath.Join(dir, "upload")
	if err := os.WriteFile(name, body, 0600); err != nil {
		t.Fatal(err)
	}
	files := &importFiles{dir: dir, limits: l, media: map[string]wpimport.MediaFile{}}
	err := files.unpack(name)
	return files, err
}
func TestImportArchiveFormatsAndSafety(t *testing.T) {
	for _, format := range []string{"zip", "tar", "tgz"} {
		t.Run(format, func(t *testing.T) {
			l := config.DefaultLimits()
			files, err := unpackImportTest(t, makeImportArchive(t, format, []importArchiveEntry{{"export.xml", testWXR, 0}, {"2026/10/photo.jpg", "photo bytes", 0}, {"__MACOSX/junk", "junk", 0}, {".DS_Store", "junk", 0}, {"Thumbs.db", "junk", 0}}), l)
			if err != nil || len(files.xml) != 1 || len(files.media) != 1 {
				t.Fatal(err, len(files.xml), files.media)
			}
			media := files.media["2026/10/photo.jpg"]
			r, err := media.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(r)
			r.Close()
			if string(body) != "photo bytes" || media.Size != 11 {
				t.Fatal(media.Size, string(body))
			}
			hosts := importSourceHosts(files.xml)
			if !hosts["old.example"] || !hosts["www.old.example"] {
				t.Fatal(hosts)
			}
			for _, entry := range []importArchiveEntry{{"../escape", "x", 0}, {"/absolute", "x", 0}, {`a\..\escape`, "x", 0}, {"link", "", tar.TypeSymlink}} {
				_, err := unpackImportTest(t, makeImportArchive(t, format, []importArchiveEntry{entry}), l)
				if err == nil {
					t.Fatal("unsafe archive accepted", entry.name)
				}
			}
			if format != "zip" {
				for _, kind := range []byte{tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo} {
					_, err := unpackImportTest(t, makeImportArchive(t, format, []importArchiveEntry{{"special", "", kind}}), l)
					if err == nil {
						t.Fatal("special entry accepted", kind)
					}
				}
			}
		})
	}
	for _, name := range []string{"x\x00y", `C:\windows\file`, "/absolute", "../parent", `\root\file`} {
		if _, err := importPath(name); err == nil {
			t.Fatal("unsafe path", name)
		}
	}
}
func TestImportArchiveCapsAndEntity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		l       func(*config.Limits)
		format  string
		entries []importArchiveEntry
		want    string
	}{
		{"entries", func(l *config.Limits) { l.ImportMaxArchiveEntries = 1 }, "zip", []importArchiveEntry{{"a", "1", 0}, {"b", "2", 0}}, "too many"},
		{"total", func(l *config.Limits) { l.ImportMaxUnpackedMB = 1 }, "tar", []importArchiveEntry{{"a", strings.Repeat("x", 1<<20), 0}, {"b", "x", 0}}, "too large"},
		{"ratio", func(l *config.Limits) {}, "zip", []importArchiveEntry{{"bomb", strings.Repeat("x", (1<<20)+1), 0}}, "ratio"},
		{"xml", func(l *config.Limits) { l.ImportMaxXMLMB = 1 }, "tar", []importArchiveEntry{{"a.xml", testWXR + strings.Repeat(" ", 1<<20), 0}}, "XML files"},
		{"entity", func(l *config.Limits) {}, "zip", []importArchiveEntry{{"a.xml", `<?xml?><!ENTITY example "x"><rss/>`, 0}}, "entity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := config.DefaultLimits()
			tc.l(&l)
			_, err := unpackImportTest(t, makeImportArchive(t, tc.format, tc.entries), l)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal(err)
			}
		})
	}
	// Extension alone does not turn a media file into XML; content sniffing does.
	files, err := unpackImportTest(t, []byte("\xef\xbb\xbf \n<rss><channel/></rss>"), config.DefaultLimits())
	if err != nil || len(files.xml) != 1 {
		t.Fatal(err)
	}
}

// Large entry checks stream bytes to disk rather than allocating the payload.
type importZeros struct{}

func (importZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestImportArchiveEntryByteCap(t *testing.T) {
	files := &importFiles{dir: t.TempDir(), limits: config.DefaultLimits(), media: map[string]wpimport.MediaFile{}}
	if err := files.entry("large.bin", io.LimitReader(importZeros{}, (200<<20)+1), 0); err == nil || !strings.Contains(err.Error(), "200 MB") {
		t.Fatal(err)
	}
}

func TestImportArchiveXMLMediaIsNotAnExport(t *testing.T) {
	const svg = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`
	files, err := unpackImportTest(t, makeImportArchive(t, "zip", []importArchiveEntry{{"export.xml", testWXR, 0}, {"2026/10/logo.svg", svg, 0}}), config.DefaultLimits())
	if err != nil || len(files.xml) != 1 || len(files.media) != 1 {
		t.Fatal(err, len(files.xml), files.media)
	}
	r, err := files.media["2026/10/logo.svg"].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	body, _ := io.ReadAll(r)
	if string(body) != svg {
		t.Fatal("XML media changed")
	}
}
