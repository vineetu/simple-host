package storage

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// FileFootprint counts deploy files only: current and vN trees. Backend
// resources, markers and saved data have separate allowances.
type FileFootprint struct {
	Live     int64
	Versions map[int]int64
}

func (f FileFootprint) Total() int64 {
	n := f.Live
	for _, b := range f.Versions {
		n += b
	}
	return n
}
func treeBytes(path string) (int64, error) {
	var n int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			n += info.Size()
		}
		return nil
	})
	return n, err
}
func footprint(path string) (FileFootprint, error) {
	f := FileFootprint{Versions: map[int]int64{}}
	dirs, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		name := d.Name()
		if name == "current" {
			f.Live, err = treeBytes(filepath.Join(path, name))
		} else if len(name) > 1 && name[0] == 'v' {
			v, e := strconv.Atoi(name[1:])
			if e != nil || v < 1 {
				continue
			}
			f.Versions[v], err = treeBytes(filepath.Join(path, name))
		}
		if err != nil {
			return f, err
		}
	}
	return f, nil
}
func (d *DiskStorage) SiteFootprint(userID, name string) (FileFootprint, error) {
	return footprint(d.SiteDir(userID, name))
}

// AccountFileBytes includes Recently deleted sites until their normal purge.
func (d *DiskStorage) AccountFileBytes(userID string) (int64, error) {
	var n int64
	for _, area := range []string{"by-id", "deleted"} {
		root := filepath.Join(d.dataDir, area, userID)
		sites, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		for _, site := range sites {
			if !site.IsDir() {
				continue
			}
			f, err := footprint(filepath.Join(root, site.Name()))
			if err != nil {
				return 0, err
			}
			n += f.Total()
		}
	}
	return n, nil
}
