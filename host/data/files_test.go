package data

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	httpx "github.com/vsriram/simple-host/host/httpx"
)

func TestPutFileRejectsExistingDirectory(t *testing.T) {
	disk := testDisk(t)
	call := Call{Owner: true, OwnerID: "owner", SiteName: "site", ResourceName: "files"}
	if err := os.MkdirAll(FilePath(disk, call, "file"), 0700); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Deps{Disk: disk, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: time.Second})
	_, auth, err := s.PutFile(context.Background(), FilePutInput{Call: call, Path: "file", Body: strings.NewReader("file")})
	if auth != nil || err == nil || err.(*Error).Code != "file_exists" {
		t.Fatalf("directory: auth=%v err=%v", auth, err)
	}
}

func TestCreateFileLink(t *testing.T) {
	disk := testDisk(t)
	key := []byte("files-test-export-key-0123456789")
	call := Call{Owner: true, OwnerID: "owner", SiteID: "site-id", SiteName: "site", ResourceName: "files"}
	file := FilePath(disk, call, "file")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Deps{Disk: disk, ExportKey: key})
	if _, err := s.CreateFileLink(context.Background(), FileLinkInput{}); err == nil || err.(*Error).Code != "forbidden" {
		t.Fatalf("non-owner: %v", err)
	}
	link, err := s.CreateFileLink(context.Background(), FileLinkInput{Call: call, Path: "file"})
	if err != nil || link.Token == "" || time.Until(link.Expires) < 9*time.Minute {
		t.Fatalf("link: %+v, %v", link, err)
	}
	if raw, err := httpx.OpenLink(key, fileLinkPurpose, link.Token); err != nil || !strings.HasPrefix(string(raw), "owner.site-id.files.") {
		t.Fatalf("sealed link: %q %v", raw, err)
	}
	os.Remove(file)
	if _, err := s.CreateFileLink(context.Background(), FileLinkInput{Call: call, Path: "file"}); err == nil || err.(*Error).Code != "file_not_found" {
		t.Fatalf("missing: %v", err)
	}
}

func TestOpenFileAccessAndRegularFile(t *testing.T) {
	disk := testDisk(t)
	call := Call{Owner: true, OwnerID: "owner", SiteName: "site", ResourceName: "files"}
	file := FilePath(disk, call, "file")
	if err := os.MkdirAll(FilePath(disk, call, "dir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Deps{Disk: disk})
	f, info, auth, err := s.OpenFile(context.Background(), FileReadInput{Call: call, Path: "file"})
	if err != nil || auth != nil || f == nil || info.Size() != 5 {
		t.Fatalf("open: file=%v info=%v auth=%v err=%v", f, info, auth, err)
	}
	f.Close()
	_, _, _, err = s.OpenFile(context.Background(), FileReadInput{Call: call, Path: "dir"})
	if e, ok := err.(*Error); !ok || e.Code != "file_not_found" {
		t.Fatalf("directory: %v", err)
	}
}

func TestDownloadFileRejectsInvalidToken(t *testing.T) {
	key := []byte("files-test-export-key-0123456789")
	s := New(nil, Deps{ExportKey: key})
	_, _, _, err := s.DownloadFile(context.Background(), httpx.SealLink(key, fileLinkPurpose, []byte("malformed")))
	var storageErr *Error
	if !errors.As(err, &storageErr) || storageErr.Code != "file_link_invalid" {
		t.Fatalf("got %v, want file_link_invalid", err)
	}
}
