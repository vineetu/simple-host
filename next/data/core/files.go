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
	return filepath.Join(d.Dir, "sites", siteID, "files", ns, name)
}

// PutFile stores one file in the namespace's file store, replacing one of
// the same name, and records it in _files with its author. The store may hold
// limits.FilesBytes in all. This is the one way a file enters a file store.
func (d *Data) PutFile(r *http.Request, n *NS, name, contentType, authorID string, body io.Reader) error {
	if name == "" || len(name) > 200 || strings.ContainsAny(name, `/\`) || strings.HasPrefix(name, ".") {
		return web.Refuse(http.StatusBadRequest, "bad_file_name", "Name the file without slashes and not starting with a dot, at most 200 characters.")
	}
	used, err := n.Query(r.Context(), `SELECT COALESCE(SUM(size), 0) FROM _files WHERE name <> ?`, name)
	if err != nil {
		return err
	}
	room := limits.FilesBytes.N - used.Rows[0][0].(int64)
	final := d.FilePath(n.Site.ID, n.Name, name)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if web.CallerOf(r).Operator {
		room = 1 << 50
	}
	size, err := io.Copy(tmp, io.LimitReader(body, max(room, 0)+1))
	tmp.Close()
	if err != nil {
		return err
	}
	if err := limits.Fits(r, limits.FilesBytes, used.Rows[0][0].(int64)+size); err != nil {
		return err
	}
	if _, err := n.Exec(r.Context(), `INSERT OR REPLACE INTO _files (name, size, type, author) VALUES (?, ?, ?, ?)`, name, size, contentType, authorID); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), final)
}
