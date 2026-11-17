package storage

import (
	"os"
	"path/filepath"
)

// setAccessMarker makes the marker durable before a protecting database write.
// Missing folders remain a no-op; protecting callers must verify presence.
func setAccessMarker(dir string, on bool) error {
	directory, err := os.Open(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	marker := filepath.Join(dir, passcodeMarker)
	if on {
		file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err = file.WriteString("this site asks for a passcode\n"); err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	return directory.Sync()
}

// SetTrashedAccessMarker repairs access before a deleted folder is reachable.
func (d *DiskStorage) SetTrashedAccessMarker(userID, siteID string, on bool) error {
	if !validPathKey(userID) || !validPathKey(siteID) {
		return os.ErrInvalid
	}
	dir := d.TrashDir(userID, siteID)
	if err := setAccessMarker(dir, on); err != nil {
		return err
	}
	if on {
		// No files is legitimate for an unpublished site, but an existing folder
		// must carry its marker before RestoreSite moves it.
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		_, err := os.Lstat(filepath.Join(dir, passcodeMarker))
		return err
	}
	return nil
}
