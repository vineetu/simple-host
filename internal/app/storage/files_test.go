package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPutFileRejectsExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	s := New(nil, Deps{
		Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: time.Second,
		ReadFile: func() ([]byte, error) { return []byte("file"), nil },
		FilePath: func(Call, string) string { return dir },
	})
	_, auth, err := s.PutFile(context.Background(), FilePutInput{Call: Call{Owner: true}})
	if auth != nil || err == nil || err.(*Error).Code != "file_exists" {
		t.Fatalf("directory: auth=%v err=%v", auth, err)
	}
}

func TestCreateFileLink(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Deps{FilePath: func(_ Call, _ string) string { return file }, FileToken: func(_ Call, path string, _ time.Time) string { return path }})
	if _, err := s.CreateFileLink(context.Background(), FileLinkInput{}); err == nil || err.(*Error).Code != "forbidden" {
		t.Fatalf("non-owner: %v", err)
	}
	link, err := s.CreateFileLink(context.Background(), FileLinkInput{Call: Call{Owner: true}, Path: "file"})
	if err != nil || link.Token != "file" || time.Until(link.Expires) < 9*time.Minute {
		t.Fatalf("link: %+v, %v", link, err)
	}
	os.Remove(file)
	if _, err := s.CreateFileLink(context.Background(), FileLinkInput{Call: Call{Owner: true}}); err == nil || err.(*Error).Code != "file_not_found" {
		t.Fatalf("missing: %v", err)
	}
}

func TestOpenFileAccessAndRegularFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Deps{FilePath: func(_ Call, _ string) string { return file }, PasscodeAllows: func() bool { return false }, ViewersAllow: func() bool { return true }})
	f, info, auth, err := s.OpenFile(context.Background(), FileReadInput{Call: Call{Owner: true}, Path: "file"})
	if err != nil || auth != nil || f == nil || info.Size() != 5 {
		t.Fatalf("open: file=%v info=%v auth=%v err=%v", f, info, auth, err)
	}
	f.Close()
	s.deps.FilePath = func(_ Call, _ string) string { return dir }
	_, _, _, err = s.OpenFile(context.Background(), FileReadInput{Call: Call{Owner: true}, Path: "dir"})
	if e, ok := err.(*Error); !ok || e.Code != "file_not_found" {
		t.Fatalf("directory: %v", err)
	}
}

func TestDownloadFileRejectsInvalidToken(t *testing.T) {
	s := New(nil, Deps{OpenFileToken: func(string) ([]byte, error) { return []byte("malformed"), nil }})
	_, _, _, err := s.DownloadFile(context.Background(), "token")
	var storageErr *Error
	if !errors.As(err, &storageErr) || storageErr.Code != "file_link_invalid" {
		t.Fatalf("got %v, want file_link_invalid", err)
	}
}
