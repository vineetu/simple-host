package storage

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMaintenanceLockChild(t *testing.T) {
	dir := os.Getenv("MAINTENANCE_TEST_DIR")
	if dir == "" {
		return
	}
	lock, err := LockMaintenance(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	os.Stdout.WriteString("locked\n")
	select {}
}

func TestMaintenanceLockProcessDeath(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMaintenanceLockChild$", "-test.timeout=1m")
	cmd.Env = append(os.Environ(), "MAINTENANCE_TEST_DIR="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() || scan.Text() != "locked" {
		t.Fatal("child did not acquire lock")
	}
	for _, exclusive := range []bool{false, true} {
		lock, err := LockMaintenance(dir, exclusive)
		if lock != nil {
			lock.Close()
			t.Fatal("overlapping lock acquired")
		}
		want := ErrMaintenanceRunning
		if exclusive {
			want = ErrDataDirInUse
		}
		if !errors.Is(err, want) {
			t.Fatalf("lock refusal: %v", err)
		}
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	lock, err := LockMaintenance(dir, true)
	if err != nil {
		t.Fatalf("lock survived process death: %v", err)
	}
	lock.Close()
}

// A lock file created by another user is typically readable and not writable
// by the server's user. A shared holder opens it read-only, so that cannot
// stop the server starting; the permission bits stand in for the other owner.
func TestMaintenanceLockSharedOpensReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".maintenance.lock")
	if err := os.WriteFile(path, nil, 0444); err != nil {
		t.Fatal(err)
	}
	// The directory is not writable either: nothing may be created.
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })
	shared, err := LockMaintenance(dir, false)
	if err != nil {
		t.Fatalf("read-only lock file stopped a shared holder: %v", err)
	}
	// It is a real lock: an exclusive holder (through its own descriptor) is refused.
	other, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err = syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("shared lock on a read-only descriptor does not exclude: %v", err)
	}
	// A command that needs the exclusive lock and cannot write the file says so.
	if lock, err := LockMaintenance(dir, true); err == nil {
		lock.Close()
		t.Fatal("exclusive lock taken on a file this user cannot write")
	} else if !strings.Contains(err.Error(), path) {
		t.Fatalf("error does not name the file: %v", err)
	}
	shared.Close()
	if err = syscall.Flock(int(other.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestMaintenanceLockCreatesWorldReadableFile(t *testing.T) {
	dir := t.TempDir()
	old := syscall.Umask(022)
	defer syscall.Umask(old)
	lock, err := LockMaintenance(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
	info, err := os.Stat(filepath.Join(dir, ".maintenance.lock"))
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("lock file mode: %v %v", info, err)
	}
}

func TestInstallIDFileIsWrittenOnce(t *testing.T) {
	dir := t.TempDir()
	if id, err := ReadInstallID(dir); id != "" || err != nil {
		t.Fatalf("absent: %q %v", id, err)
	}
	if err := WriteInstallID(dir, "first"); err != nil {
		t.Fatal(err)
	}
	if id, err := ReadInstallID(dir); id != "first" || err != nil {
		t.Fatalf("read back: %q %v", id, err)
	}
	if err := WriteInstallID(dir, "second"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second write: %v", err)
	}
	if id, _ := ReadInstallID(dir); id != "first" {
		t.Fatalf("identity rewritten: %q", id)
	}
	if err := os.WriteFile(InstallIDPath(dir), []byte(" \n"), 0644); err != nil {
		t.Fatal(err)
	}
	if id, err := ReadInstallID(dir); err == nil {
		t.Fatalf("empty file read as %q", id)
	}
}
