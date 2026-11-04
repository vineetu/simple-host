package storage

import "os"

// CreateImportDir creates the private staging directory for an import.
func CreateImportDir() (string, error) { return os.MkdirTemp("", "simple-host-import-") }

// RemoveImportDir removes an import's staging files.
func RemoveImportDir(dir string) error { return os.RemoveAll(dir) }

// CreateImportFile creates a temporary upload or generated archive in the staging directory.
func CreateImportFile(dir, pattern string) (*os.File, error) { return os.CreateTemp(dir, pattern) }
