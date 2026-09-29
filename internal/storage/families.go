package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// Address families (handler/familyhost.go): families/<suffix> -> by-id/<user>
// lets nginx serve <label>.<suffix> straight from the account's site
// folders (by-id/<user>/<prefix><label>/current). Keyed by the account id, so
// a handle change never breaks it; removing the link stops serving at once.

// FamilyDir is the families farm.
func (d *DiskStorage) FamilyDir() string { return filepath.Join(d.dataDir, "families") }

// BindFamily creates families/<suffix> -> ../by-id/<userID>. Idempotent when
// it already points there; a link to another account is replaced (the
// database decides who holds a family; a stale link must not outlive it).
func (d *DiskStorage) BindFamily(userID, suffix string) error {
	if !validPathKey(userID) || !validDomainKey(suffix) {
		return fmt.Errorf("invalid family %q for %q", suffix, userID)
	}
	dir := d.FamilyDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create families dir: %w", err)
	}
	link := filepath.Join(dir, suffix)
	target := filepath.Join("..", "by-id", userID)
	if info, err := os.Lstat(link); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("family path %q exists and is not a symlink; refusing to clobber", link)
		}
		if cur, _ := os.Readlink(link); cur == target {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("remove stale family link: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat family path: %w", err)
	}
	// The account's folder exists before its first site does, so nginx and
	// the issuer see a link that resolves.
	if err := os.MkdirAll(filepath.Join(d.dataDir, "by-id", userID), 0o755); err != nil {
		return fmt.Errorf("create account dir: %w", err)
	}
	return os.Symlink(target, link)
}

// UnbindFamily removes families/<suffix>. Missing is fine; anything that is
// not a symlink is left alone.
func (d *DiskStorage) UnbindFamily(suffix string) error {
	if !validDomainKey(suffix) {
		return fmt.Errorf("invalid family %q", suffix)
	}
	link := filepath.Join(d.FamilyDir(), suffix)
	info, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("family path %q is not a symlink; refusing to remove", link)
	}
	return os.Remove(link)
}

// FamilyLinkTarget is where families/<suffix> points ("" when absent).
func (d *DiskStorage) FamilyLinkTarget(suffix string) string {
	if !validDomainKey(suffix) {
		return ""
	}
	t, err := os.Readlink(filepath.Join(d.FamilyDir(), suffix))
	if err != nil {
		return ""
	}
	return t
}

// livesElsewhereMarker marks a site whose home is a domain of its own (a
// custom domain or a claimed name): the family vhost hands its requests to
// the app, which redirects there, instead of serving the file.
const livesElsewhereMarker = "lives-elsewhere"

// SetLivesElsewhere writes (domain != "") or removes the marker. Never
// creates a missing site folder.
func (d *DiskStorage) SetLivesElsewhere(userID, siteName, domain string) error {
	if !validPathKey(userID) || !validPathKey(siteName) {
		return fmt.Errorf("invalid site %q/%q", userID, siteName)
	}
	p := filepath.Join(d.SiteDir(userID, siteName), livesElsewhereMarker)
	if domain == "" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if !validDomainKey(domain) {
		return fmt.Errorf("invalid domain %q", domain)
	}
	if _, err := os.Stat(d.SiteDir(userID, siteName)); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return os.WriteFile(p, []byte(domain+"\n"), 0o644)
}
