package accesstest

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
)

// acctZipFiles reads a .zip into name -> body.
func acctZipFiles(t *testing.T, b []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip (%d bytes): %v", len(b), err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(body)
	}
	return out
}

// acctFileUnder returns the archive entry whose name ends in suffix.
func acctFileUnder(t *testing.T, files map[string]string, suffix string) string {
	t.Helper()
	for name, body := range files {
		if strings.HasSuffix(name, suffix) {
			return body
		}
	}
	t.Fatalf("archive has no %s", suffix)
	return ""
}

// acctSoftDelete moves p's site to Recently deleted, as the owner's delete does.
func (a *App) acctSoftDelete(t *testing.T, userID, name string) {
	t.Helper()
	if err := a.Access.SiteFiles.Delete(context.Background(), sites.DeleteInput{UserID: userID, Name: name}); err != nil {
		t.Fatal(err)
	}
}
