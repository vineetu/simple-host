package analytics

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writePlain(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGz(t *testing.T, path, body string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	if _, err := zw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// A rebuild must replay logrotate's archives oldest first, numerically (10
// before 9, which a string sort gets wrong), then the live file.
func TestReplayFilesLogrotateOrder(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "analytics.log")
	writePlain(t, live, "live\n")
	writePlain(t, live+".1", "one\n")
	for _, n := range []string{"2", "9", "10"} {
		writeGz(t, live+"."+n+".gz", n+"\n")
	}
	writePlain(t, live+".bak", "not an archive\n")
	writePlain(t, filepath.Join(dir, "other.log.3.gz"), "")

	got := ReplayFiles(live)
	want := []string{live + ".10.gz", live + ".9.gz", live + ".2.gz", live + ".1", live}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReplayFiles = %v\nwant %v", got, want)
	}
}

// Caddy's rolls sort by their timestamp; a roll caught mid-compression is
// replayed once, from the plain copy.
func TestReplayFilesCaddyOrder(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "access.log")
	writePlain(t, live, "")
	older := filepath.Join(dir, "access-2026-09-01T00-00-00.000-time.log.gz")
	newer := filepath.Join(dir, "access-2026-09-02T00-00-00.000-size.log")
	writeGz(t, older, "a\n")
	writePlain(t, newer, "b\n")
	writeGz(t, newer+".gz", "b\n")
	writePlain(t, filepath.Join(dir, "access-notes.log"), "")

	got := ReplayFiles(live)
	want := []string{older, newer, live}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReplayFiles = %v\nwant %v", got, want)
	}
}

// Offsets count uncompressed bytes, so resuming inside a gzip archive must
// land on the same line as resuming inside the plain file.
func TestReadLinesGzipResumes(t *testing.T) {
	dir := t.TempDir()
	body := "first\nsecond\nthird\npartial"
	plain := filepath.Join(dir, "a.log")
	gz := filepath.Join(dir, "a.log.gz")
	writePlain(t, plain, body)
	writeGz(t, gz, body)

	for _, p := range []string{plain, gz} {
		lines, off, err := readLines(p, int64(len("first\n")), 10)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !reflect.DeepEqual(lines, []string{"second", "third"}) || off != int64(len("first\nsecond\nthird\n")) {
			t.Errorf("%s: lines=%q off=%d", p, lines, off)
		}
	}
	// An offset past the end of a gzip archive reads nothing and keeps it.
	lines, off, err := readLines(gz, 1000, 10)
	if err != nil || len(lines) != 0 || off != 1000 {
		t.Errorf("past end: lines=%q off=%d err=%v", lines, off, err)
	}
}

// When Caddy rolls the access log there is no .1. The ingester must finish
// the file it was reading from the stored offset, whether the roll is still
// plain (same inode) or already gzipped (new inode), instead of dropping it.
func TestPreviousLogCaddyRoll(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "access.log")
	writePlain(t, live, "read\nunread\n")
	info, _ := os.Stat(live)
	oldInode := fileInode(info)
	storedOffset := int64(len("read\n"))

	// Caddy renames the live file and opens a new one.
	roll := filepath.Join(dir, "access-2026-09-27T00-00-00.000-time.log")
	if err := os.Rename(live, roll); err != nil {
		t.Fatal(err)
	}
	writePlain(t, live, "new\n")
	writeGz(t, filepath.Join(dir, "access-2026-09-26T00-00-00.000-time.log.gz"), "older roll\n")

	ing := &Ingester{logPath: live}
	path, inode, from, ok := ing.previousLog(oldInode, storedOffset)
	if !ok || path != roll || inode != oldInode || from != storedOffset {
		t.Fatalf("plain roll: path=%s inode=%d from=%d ok=%v", path, inode, from, ok)
	}

	// Then gzips it and removes the plain copy.
	writeGz(t, roll+".gz", "read\nunread\n")
	os.Remove(roll)
	path, _, from, ok = ing.previousLog(oldInode, storedOffset)
	if !ok || path != roll+".gz" || from != storedOffset {
		t.Fatalf("gz roll: path=%s from=%d ok=%v", path, from, ok)
	}
	lines, _, err := readLines(path, from, 10)
	if err != nil || !reflect.DeepEqual(lines, []string{"unread"}) {
		t.Errorf("unread tail = %q, %v", lines, err)
	}
}

// logrotate's .1 still wins when present, resuming only on a matching inode.
func TestPreviousLogLogrotate(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "analytics.log")
	writePlain(t, live+".1", "a\nb\n")
	writePlain(t, live, "")
	info, _ := os.Stat(live + ".1")
	ing := &Ingester{logPath: live}

	if path, _, from, ok := ing.previousLog(fileInode(info), 2); !ok || path != live+".1" || from != 2 {
		t.Errorf("matching inode: %s from %d ok=%v", path, from, ok)
	}
	if _, _, from, ok := ing.previousLog(fileInode(info)+12345, 2); !ok || from != 0 {
		t.Errorf("other inode should read .1 from 0, got from %d ok=%v", from, ok)
	}
	os.Remove(live + ".1")
	if _, _, _, ok := ing.previousLog(1, 2); ok {
		t.Error("no predecessor should report ok=false")
	}
}
