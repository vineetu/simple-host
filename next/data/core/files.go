package data

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"simplehost/core/limits"
	"simplehost/core/web"
)

// FilePath is where a stored file of a namespace lives on disk:
// Dir/sites/<site id>/files/<namespace>/<name>.
func (d *Data) FilePath(siteID, ns, name string) string {
	return filepath.Join(d.Dir, "sites", siteID, "files", ns, filepath.FromSlash(name))
}

// ValidFileName refuses a name that is not a relative path of plain parts
// (photos/cat.jpg). Published pages already use paths with folders.
func ValidFileName(name string) error {
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(name) > 512 || strings.ContainsAny(name, "\x00\\") {
			return web.Refuse(http.StatusBadRequest, "bad_file_name", "Name the file with a relative path such as photos/cat.jpg, at most 512 characters.")
		}
	}
	return nil
}

// PutFile stores one file in the namespace's file store, replacing one of
// the same name, and records it in _files (a replaced file keeps its first
// author; "" is nobody). The store may hold limits.FilesBytes in all. This is
// the one way a file enters a file store: owners' upload links and visitors'
// uploads alike.
func (d *Data) PutFile(r *http.Request, n *NS, name, contentType, authorID string, body io.Reader) error {
	if err := ValidFileName(name); err != nil {
		return err
	}
	sum, err := n.Query(r.Context(), `SELECT COALESCE(SUM(size), 0) FROM _files WHERE name <> ?`, name)
	if err != nil {
		return err
	}
	used := sum.Rows[0][0].(int64)
	room := limits.FilesBytes.N - used
	if web.CallerOf(r).Operator {
		room = 1 << 50
	}
	final := d.FilePath(n.Site.ID, n.Name, name)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	size, err := io.Copy(tmp, io.LimitReader(body, max(room, 0)+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
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
