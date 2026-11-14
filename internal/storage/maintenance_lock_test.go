package storage

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
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
