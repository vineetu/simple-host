package data

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"simplehost/core/limits"
	"simplehost/core/web"
)

// FilePath is where a stored file of a namespace lives on disk:
// Dir/sites/<site id>/files/<namespace>/<name>. It is checked here, where
// every read, write and delete gets it, since a _files row is the owner's
// to write with SQL: a name that is not a plain relative path, or that would
// leave the namespace's folder, is refused (a route answers it as missing).
func (d *Data) FilePath(siteID, ns, name string) (string, error) {
	if err := ValidFileName(name); err != nil {
		return "", err
	}
	dir := filepath.Join(d.Dir, "sites", siteID, "files", ns)
	p := filepath.Join(dir, filepath.FromSlash(name))
	if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
		return "", errBadFileName
	}
	return p, nil
}

// running is the room each site's running uploads have taken.
var running = struct {
	sync.Mutex
	taken map[string]int64
}{taken: map[string]int64{}}

// takeRoom measures the site's file stores on disk (leaving out the file
// being replaced) and takes room for one upload: its declared length, or all
// that is left when it gives none. done gives it back.
func (d *Data) takeRoom(siteID, replaced string, declared int64, operator bool) (used, room int64, done func()) {
	running.Lock()
	defer running.Unlock()
	filepath.WalkDir(filepath.Join(d.Dir, "sites", siteID, "files"), func(p string, e fs.DirEntry, err error) error {
		if err == nil && e.Type().IsRegular() && p != replaced && !strings.HasPrefix(e.Name(), ".upload-") {
			if info, err := e.Info(); err == nil {
				used += info.Size()
			}
		}
		return nil
	})
	used += running.taken[siteID]
	room = max(limits.FilesBytes.N-used, 0)
	if operator {
		room = 1 << 50
	}
	if declared >= 0 && declared < room {
		room = declared
	}
	running.taken[siteID] += room
	return used, room, func() {
		running.Lock()
		defer running.Unlock()
		if running.taken[siteID] -= room; running.taken[siteID] <= 0 {
			delete(running.taken, siteID)
		}
	}
}

var errBadFileName = web.Refuse(http.StatusBadRequest, "bad_file_name", "Name the file with a relative path such as photos/cat.jpg, at most 512 characters.")

// ValidFileName refuses a name that is not a relative path of plain parts
// (photos/cat.jpg). Published pages already use paths with folders.
func ValidFileName(name string) error {
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(name) > 512 || strings.ContainsAny(name, "\x00\\") {
			return errBadFileName
		}
	}
	return nil
}

// PutFile stores one file in the namespace's file store, replacing one of
// the same name, and records it in _files (a replaced file keeps its first
// author; "" is nobody). A site's file stores may hold limits.FilesBytes in
// all, measured on disk (never from _files, which the owner may rewrite):
// what is stored, plus the room uploads still running have taken. This is
// the one way a file enters a file store: owners' upload links and visitors'
// uploads alike.
func (d *Data) PutFile(r *http.Request, n *NS, name, contentType, authorID string, body io.Reader) error {
	final, err := d.FilePath(n.Site.ID, n.Name, name)
	if err != nil {
		return err
	}
	used, room, done := d.takeRoom(n.Site.ID, final, r.ContentLength, web.CallerOf(r).Operator)
	defer done()
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	size, err := io.Copy(tmp, io.LimitReader(body, room+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if size > room {
		used = limits.FilesBytes.N // more than it said, or than is left
	}
	if err := limits.Fits(r, limits.FilesBytes, used+size); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return err
	}
	var author any
	if authorID != "" {
		author = authorID
	}
	_, err = n.Exec(r.Context(), `INSERT INTO _files (name, size, type, visitor_id) VALUES (?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET size = excluded.size, type = excluded.type, created_at = CURRENT_TIMESTAMP`, name, size, contentType, author)
	return err
}
