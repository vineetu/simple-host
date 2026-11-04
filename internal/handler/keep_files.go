package handler

import "errors"

func normalizeFilePatch(files map[string][]byte, remove []string) (map[string]bool, error) {
	removed := map[string]bool{}
	for _, raw := range remove {
		clean, err := importPath(raw)
		if err != nil || clean == "" {
			return nil, errors.New("unsafe path in remove_files")
		}
		removed[clean] = true
	}
	// Normalize the patch before merging so ./about.html replaces about.html.
	patch := map[string][]byte{}
	for raw, b := range files {
		clean, err := importPath(raw)
		if err != nil || clean == "" {
			return nil, errors.New("unsafe file path")
		}
		if _, ok := patch[clean]; ok {
			return nil, errors.New("duplicate file path")
		}
		patch[clean] = b
		delete(files, raw)
	}
	for p, b := range patch {
		files[p] = b
	}
	return removed, nil
}
