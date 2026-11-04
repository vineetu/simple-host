package operations

import (
	"context"
	"encoding/json"
	db "github.com/vsriram/simple-host/internal/repository/operations"
	"github.com/vsriram/simple-host/internal/storage"
	"os"
	"path/filepath"
	"testing"
)

func TestImageScanEvidenceSymlink(t *testing.T) {
	data := t.TempDir()
	disk, err := storage.NewDiskStorage(data)
	if err != nil {
		t.Fatal(err)
	}
	siteDir := disk.SiteDir("u1", "s1")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "evidence-link")
	if err := os.Symlink(siteDir, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{link, filepath.Join(link, "not-yet")} {
		s := &ImageScan{disk: disk, Config: ImageScanConfig{EvidenceDir: dir}}
		if why := s.evidenceDirRefused(); why == "" {
			t.Fatalf("%s accepted", dir)
		}
	}
	s := &ImageScan{disk: disk, Config: ImageScanConfig{EvidenceDir: filepath.Join(data, ".evidence")}}
	if why := s.evidenceDirRefused(); why != "" {
		t.Fatalf("default refused: %s", why)
	}
}
func TestImageScanRawJSON(t *testing.T) {
	if got := rawJSON(json.RawMessage(`{"IsMatch":true}`)); string(got) != `{"IsMatch":true}` {
		t.Fatalf("valid reply changed: %s", got)
	}
	got := rawJSON(json.RawMessage(`{"IsMatch":tr`))
	var s string
	if !json.Valid(got) || json.Unmarshal(got, &s) != nil || s != `{"IsMatch":tr` {
		t.Fatalf("cut reply: %s", got)
	}
}
func TestImageScanReporteeNoHoldingAccount(t *testing.T) {
	s := &ImageScan{}
	m := db.ImageMatch{Kind: "site_file", OwnerHandle: "ev-holding", OwnerEmail: "ev-holding" + holdingAccountSuffix}
	if got := s.ReporteeOf(context.Background(), m); got != "uploader not recorded" {
		t.Fatalf("reportee %q, want %q", got, "uploader not recorded")
	}
	m = db.ImageMatch{Kind: "storage_file", WriterID: "owner", OwnerHandle: "ev-holding", OwnerEmail: "ev-holding" + holdingAccountSuffix}
	if got := s.ReporteeOf(context.Background(), m); got != "uploader not recorded" {
		t.Fatalf("owner write: reportee %q, want %q", got, "uploader not recorded")
	}
}
