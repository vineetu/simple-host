package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	ErrMaintenanceRunning = errors.New("a maintenance command is running on this data directory")
	ErrDataDirInUse       = errors.New("the server (or another maintenance command) is using this data directory; stop it first")
	// ErrDataDirOwner is returned when a maintenance command is run by a user
	// who does not own the data directory.
	ErrDataDirOwner = errors.New("this user does not own the data directory")
)

const (
	maintenanceLockName = ".maintenance.lock"
	installIDName       = ".install-id"
)

// LockMaintenance holds a nonblocking process lock until the returned file is
// closed. Servers use shared locks; commands that rewrite data use exclusive ones.
//
// A shared holder opens the file read-only: the file holds nothing, flock needs
// no write access, and a lock file created by another user (root running a
// command once) must not stop the server starting. The file is created
// world-readable for the same reason. A file that cannot be opened is reported
// with its path and owner.
func LockMaintenance(dataDir string, exclusive bool) (*os.File, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, maintenanceLockName)
	flags := os.O_CREATE | os.O_RDONLY
	if exclusive {
		flags = os.O_CREATE | os.O_RDWR
	}
	f, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return nil, fmt.Errorf("open the maintenance lock file %s (%s): %w", path, fileOwner(path), err)
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	if err = syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			if exclusive {
				return nil, ErrDataDirInUse
			}
			return nil, ErrMaintenanceRunning
		}
		return nil, err
	}
	return f, nil
}

// fileOwner describes who owns path, for an error message. Stat needs no read
// access to the file itself.
func fileOwner(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "owner unknown"
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "owner unknown"
	}
	return fmt.Sprintf("owned by uid %d, mode %04o; this process runs as uid %d", st.Uid, info.Mode().Perm(), os.Geteuid())
}

// CheckDataDirOwner refuses a data directory that euid does not own. A command
// run as another user (root, usually) would leave directories and files the
// server's user cannot open. It reads only; a missing directory is refused too.
func CheckDataDirOwner(dataDir string, euid int) error {
	info, err := os.Stat(dataDir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDataDirOwner, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory whose owner can be read", ErrDataDirOwner, dataDir)
	}
	if int(st.Uid) != euid {
		return fmt.Errorf("%w: %s is owned by uid %d and this command runs as uid %d; run it as the user the server runs as", ErrDataDirOwner, dataDir, st.Uid, euid)
	}
	return nil
}

// InstallIDPath is the file that carries the install identity. The same value
// is the single row of install_identity in the install's database.
func InstallIDPath(dataDir string) string { return filepath.Join(dataDir, installIDName) }

// ReadInstallID returns the identity saved in the data directory, or "" when
// the file is absent. It never writes.
func ReadInstallID(dataDir string) (string, error) {
	b, err := os.ReadFile(InstallIDPath(dataDir))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if id == "" {
		return "", fmt.Errorf("%s is empty", InstallIDPath(dataDir))
	}
	return id, nil
}

// WriteInstallID saves the identity once: it fails if the file exists, and the
// file is never rewritten. The file and its directory entry are synced.
func WriteInstallID(dataDir, id string) error {
	path := InstallIDPath(dataDir)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(id + "\n")
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return err
	}
	return SyncDir(dataDir)
}

// SyncDir makes a rename, creation or removal inside dir durable.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if closeErr := d.Close(); err == nil {
		err = closeErr
	}
	return err
}
