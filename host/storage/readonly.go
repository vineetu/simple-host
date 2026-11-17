package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// OpenReadOnly opens an existing site tree without creating directories or
// cleaning legacy markers. Static only calls its serving methods.
func OpenReadOnly(dataDir string) (*DiskStorage, error) {
	info, err := os.Stat(dataDir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("data directory is not a directory")
	}
	return &DiskStorage{dataDir: dataDir}, nil
}

// LockMaintenanceReadOnly participates in the existing maintenance protocol
// without creating its file. The API or installer creates that file first.
func LockMaintenanceReadOnly(dataDir string) (*os.File, error) {
	f, err := os.Open(filepath.Join(dataDir, maintenanceLockName))
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, ErrMaintenanceRunning
	}
	return f, nil
}
