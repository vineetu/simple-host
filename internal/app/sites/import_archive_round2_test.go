package sites

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/edition"
)

func TestImportTARMetadataPayloadBudget(t *testing.T) {
	const valueSize = (64 << 10) - 32
	files, err := unpackImportTest(t, physicalPAX(t, 16, valueSize), edition.ForKind(edition.Host).Limits())
	if err != nil {
		t.Fatal("payload within metadata budget rejected:", err)
	}
	if want := int64(16 * len(paxBody(strings.Repeat("x", valueSize)))); files.metadata != want {
		t.Fatalf("charged %d metadata bytes, want %d payload bytes", files.metadata, want)
	}
	if _, err := unpackImportTest(t, physicalPAX(t, 21, 64<<10), edition.ForKind(edition.Host).Limits()); err == nil || !strings.Contains(err.Error(), "extended headers") {
		t.Fatal("oversized metadata payload accepted:", err)
	}
}

func TestImportGNUPAX5000Photos(t *testing.T) {
	var body bytes.Buffer
	w := tar.NewWriter(&body)
	stamp := time.Unix(1700000000, 123456789)
	for i := 0; i <= 5000; i++ {
		name, contents := fmt.Sprintf("photos/%04d.jpg", i), "x"
		if i == 5000 {
			name, contents = "export.xml", testWXR
		}
		// GNU tar --format=pax preserves nanosecond mtime/atime/ctime.
		h := &tar.Header{Name: name, Mode: 0644, Size: int64(len(contents)), Format: tar.FormatPAX,
			ModTime: stamp, AccessTime: stamp, ChangeTime: stamp}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := unpackImportTest(t, body.Bytes(), edition.ForKind(edition.Host).Limits())
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Media) != 5000 || len(files.Xml) != 1 || files.entries != 10002 {
		t.Fatalf("media=%d XML=%d physical entries=%d", len(files.Media), len(files.Xml), files.entries)
	}
}

func TestImportZIP64OrdinaryEOCDFields(t *testing.T) {
	body := makeImportArchive(t, "zip", []importArchiveEntry{{"export.xml", testWXR, 0}})
	end := len(body) - 22
	e := bytes.Clone(body[end:])
	z := make([]byte, 56)
	binary.LittleEndian.PutUint32(z, 0x06064b50)
	binary.LittleEndian.PutUint64(z[4:], 44)
	binary.LittleEndian.PutUint64(z[24:], 1)
	binary.LittleEndian.PutUint64(z[32:], 1)
	binary.LittleEndian.PutUint64(z[40:], uint64(binary.LittleEndian.Uint32(e[12:])))
	binary.LittleEndian.PutUint64(z[48:], uint64(binary.LittleEndian.Uint32(e[16:])))
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(end))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	prefix := append(bytes.Clone(body[:end]), z...)
	prefix = append(prefix, locator...)
	for _, tc := range []struct {
		name   string
		change func([]byte)
		valid  bool
	}{
		{"ordinary fields", func([]byte) {}, true},
		{"sentinel count", func(e []byte) {
			binary.LittleEndian.PutUint16(e[8:], 0xffff)
			binary.LittleEndian.PutUint16(e[10:], 0xffff)
		}, true},
		{"sentinel disk count", func(e []byte) { binary.LittleEndian.PutUint16(e[8:], 0xffff) }, true},
		{"sentinel total count", func(e []byte) { binary.LittleEndian.PutUint16(e[10:], 0xffff) }, true},
		{"sentinel size", func(e []byte) { binary.LittleEndian.PutUint32(e[12:], 0xffffffff) }, true},
		{"sentinel offset", func(e []byte) { binary.LittleEndian.PutUint32(e[16:], 0xffffffff) }, true},
		{"count mismatch", func(e []byte) {
			binary.LittleEndian.PutUint16(e[8:], 2)
			binary.LittleEndian.PutUint16(e[10:], 2)
		}, false},
		{"disk count mismatch", func(e []byte) { binary.LittleEndian.PutUint16(e[8:], 2) }, false},
		{"size mismatch", func(e []byte) { binary.LittleEndian.PutUint32(e[12:], 0) }, false},
		{"offset mismatch", func(e []byte) { binary.LittleEndian.PutUint32(e[16:], 0) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := bytes.Clone(e)
			tc.change(e)
			archive := append(bytes.Clone(prefix), e...)
			files, err := unpackImportTest(t, archive, edition.ForKind(edition.Host).Limits())
			if tc.valid {
				if err != nil || len(files.Xml) != 1 {
					t.Fatal("valid ZIP64 rejected:", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "central directory") {
				t.Fatal("inconsistent ZIP64 accepted:", err)
			}
		})
	}
}
