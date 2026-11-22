package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/host/sites"
)

func mergePhotos(ctx context.Context, files map[string][]byte, photos []Photo, reconcile bool) ([]string, error) {
	paths := make([]string, 0, len(photos))
	if reconcile {
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		used := map[string]bool{}
		for _, p := range photos {
			found := "images/" + p.Name
			if used[found] || !bytes.Equal(files[found], p.Data) {
				found = ""
				for _, name := range names {
					if !used[name] && matchesPhotoName(name, p.Name) && bytes.Equal(files[name], p.Data) {
						found = name
						break
					}
				}
			}
			if found == "" {
				return nil, &sites.Error{Code: "photo_save_uncertain", Message: "Could not confirm this batch. Check the site before adding these photos again."}
			}
			used[found] = true
			paths = append(paths, found)
		}
		return paths, nil
	}
	occupied, err := photoOccupiedPaths(ctx, files)
	if err != nil {
		return nil, &sites.Error{Code: "photo_allocation_cancelled", Message: err.Error()}
	}
	for _, p := range photos {
		path, err := nextPhotoPath(ctx, occupied, p.Name)
		if err != nil {
			if ctx.Err() != nil {
				return nil, &sites.Error{Code: "photo_allocation_cancelled", Message: err.Error()}
			}
			return nil, &sites.Error{Code: "photo_conflict", Message: err.Error()}
		}
		occupied[path] = false
		files[path] = p.Data
		paths = append(paths, path)
	}
	return paths, nil
}
func matchesPhotoName(path, name string) bool {
	if path == "images/"+name {
		return true
	}
	ext := name[strings.LastIndexByte(name, '.'):]
	if !strings.HasPrefix(path, "images/") || !strings.HasSuffix(path, ext) {
		return false
	}
	stem := strings.TrimSuffix(strings.TrimPrefix(path, "images/"), ext)
	at := strings.LastIndexByte(stem, '-')
	if at < 0 {
		return false
	}
	n, err := strconv.Atoi(stem[at+1:])
	if err != nil || n < 2 || strconv.Itoa(n) != stem[at+1:] {
		return false
	}
	base := strings.TrimSuffix(name, ext)
	if max := 60 - len(stem[at:]); len(base) > max {
		base = base[:max]
	}
	return stem[:at] == base
}

// The value distinguishes directories from files. Build prefixes once, then
// each suffix probe is one map lookup, even in a large, collision-heavy tree.
func photoOccupiedPaths(ctx context.Context, files map[string][]byte) (map[string]bool, error) {
	occupied := make(map[string]bool, len(files))
	for path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		occupied[path] = false
		for i := strings.IndexByte(path, '/'); i >= 0; {
			occupied[path[:i]] = true
			next := strings.IndexByte(path[i+1:], '/')
			if next < 0 {
				break
			}
			i += next + 1
		}
	}
	return occupied, nil
}

func nextPhotoPath(ctx context.Context, occupied map[string]bool, name string) (string, error) {
	if dir, exists := occupied["images"]; exists && !dir {
		return "", errors.New("The images path is a file. Rename it before adding photos.")
	}
	ext := name[strings.LastIndexByte(name, '.'):]
	stem := strings.TrimSuffix(name, ext)
	path := "images/" + name
	for n := 2; ; n++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		dir, taken := occupied[path]
		if !taken {
			return path, nil
		}
		if dir {
			return "", errors.New("A folder uses this photo name. Choose another name.")
		}
		suffix := fmt.Sprintf("-%d", n)
		base := stem
		if len(base)+len(suffix) > 60 {
			base = base[:60-len(suffix)]
		}
		path = "images/" + base + suffix + ext
	}
}
