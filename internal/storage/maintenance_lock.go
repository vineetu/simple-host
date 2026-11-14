package storage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var (
	ErrMaintenanceRunning = errors.New("a maintenance command is running on this data directory")
	ErrDataDirInUse       = errors.New("the server (or another maintenance command) is using this data directory; stop it first")
)

// LockMaintenance holds a nonblocking process lock until the returned file is
// closed. Servers use shared locks; commands that rewrite data use exclusive ones.
func LockMaintenance(dataDir string, exclusive bool) (*os.File, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dataDir, ".maintenance.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
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
