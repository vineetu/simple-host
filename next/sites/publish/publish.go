// Package publish is route 22: send a site's files (as JSON or as one
// archive) and get its live address back. A bad upload is refused whole; the
// live site is never half-updated.
package publish

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"simplehost/app"
	"simplehost/core/limits"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

type handler struct{ a *app.App }

// Mount adds route 22. POST answers exactly as PUT does: scripts use both.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("PUT /v1/sites/{site}", web.Func(h.publish))
	a.Routes.Platform.Handle("POST /v1/sites/{site}", web.Func(h.publish))
}

// publish reads the body (JSON files, or a .tar.gz or .zip archive), makes
// the site if it is new, and puts the files live as the next version. With
// ?keep_other_files=true the live files not sent stay.
func (h *handler) publish(w http.ResponseWriter, r *http.Request) error {
	done, err := limits.Enter(r, limits.Publishes) // a body is held in memory: only a few at once
	if err != nil {
		return err
	}
	defer done()
	site, ownerID, err := h.target(r)
	if err != nil {
		return err
	}
	files, err := read(w, r)
	if err != nil {
		return err
	}
	n, err := h.put(r, site, ownerID, files, r.URL.Query().Get("keep_other_files") == "true")
	if err != nil {
		return err
	}
	if site == nil {
		site, err = h.a.Sites.Get(r.Context(), ownerID, r.PathValue("site"))
		if err != nil {
			return err
		}
	}
	out := map[string]any{"url": h.a.Sites.URL(site), "version": n, "site": site.Name}
	if h.a.Sites.Cloudflare != nil {
		out["note"] = "Visitors may see the previous version for up to a minute."
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

// target is the site to publish to (nil when it is new) and its owner. The
// caller must act for the owner: themselves, or the team named by ?owner=.
func (h *handler) target(r *http.Request) (*sites.Site, string, error) {
	site, err := h.a.Sites.Of(r)
	if err == nil {
		return site, site.OwnerID, h.a.Sites.Manage(r, site)
	}
	if !errors.Is(err, web.NotFound) {
		return nil, "", err
	}
	c := web.CallerOf(r)
	ownerID := c.ID
	if handle := r.URL.Query().Get("owner"); handle != "" {
		o, err := h.a.People.ByHandle(r.Context(), handle)
		if err != nil {
			return nil, "", err
		}
		ownerID = o.ID
	}
	if ok, err := h.a.People.ActsFor(r.Context(), c, ownerID); err != nil || !ok {
		if err == nil {
			err = web.Refuse(http.StatusForbidden, "not_yours", "Only the team's members can publish its sites.")
		}
		return nil, "", err
	}
	return nil, ownerID, nil
}

// read turns the body into files (path -> contents), refusing as soon as
// they pass the site size limit.
func read(w http.ResponseWriter, r *http.Request) (map[string][]byte, error) {
	room := limits.SiteBytes.N
	if web.CallerOf(r).Operator {
		room = 1 << 50
	}
	tooBig := func() error { return limits.Fits(r, limits.SiteBytes, limits.SiteBytes.N+1) }
	isJSON := strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
	if isJSON && r.ContentLength > room/3*4+1<<20 || !isJSON && r.ContentLength > room {
		return nil, tooBig() // refused by its declared size, before a byte is read
	}
	if isJSON {
		var in struct {
			Files       map[string]string `json:"files"`
			FilesBase64 map[string]string `json:"files_base64"`
		}
		var big *http.MaxBytesError
		err := json.NewDecoder(http.MaxBytesReader(w, r.Body, room/3*4+1<<20)).Decode(&in)
		if errors.As(err, &big) {
			return nil, tooBig()
		}
		if err != nil {
			return nil, web.Refuse(http.StatusBadRequest, "bad_json", `Send {"files": {"index.html": "..."}} and binary files in "files_base64".`)
		}
		files := map[string][]byte{}
		for p, s := range in.Files {
			files[p] = []byte(s)
		}
		for p, s := range in.FilesBase64 {
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, web.Refuse(http.StatusBadRequest, "bad_base64", "The file "+p+" in files_base64 is not valid base64.")
			}
			files[p] = b
		}
		return files, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, room+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > room {
		return nil, tooBig()
	}
	badArchive := web.Refuse(http.StatusBadRequest, "bad_archive", "Send a .tar.gz or .zip archive, or JSON with Content-Type: application/json.")
	files := map[string][]byte{}
	add := func(name string, rd io.Reader) error {
		b, err := io.ReadAll(io.LimitReader(rd, room+1))
		if err != nil {
			return badArchive
		}
		if room -= int64(len(b)); room < 0 {
			return tooBig()
		}
		files[name] = b
		return nil
	}
	switch {
	case bytes.HasPrefix(body, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, badArchive
		}
		for _, f := range zr.File {
			if !f.Mode().IsRegular() {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, badArchive
			}
			err = add(f.Name, rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
		}
	case bytes.HasPrefix(body, []byte{0x1f, 0x8b}):
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, badArchive
		}
		tr := tar.NewReader(gz)
		for {
			hd, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, badArchive
			}
			if hd.Typeflag == tar.TypeReg {
				if err := add(hd.Name, tr); err != nil {
					return nil, err
				}
			}
		}
	default:
		return nil, badArchive
	}
	return files, nil
}

// put writes files as the site's next version, with the live files not sent
// when keep is set, and puts it live; a new site (nil) is made first. Nothing
// changes unless all of it fits.
func (h *handler) put(r *http.Request, site *sites.Site, ownerID string, sent map[string][]byte, keep bool) (int, error) {
	files := map[string][]byte{}
	var total int64
	for p, b := range sent {
		clean, ok := safePath(p)
		if !ok {
			return 0, web.Refuse(http.StatusBadRequest, "bad_path", fmt.Sprintf("The path %q is not allowed. Use relative paths without .. or control characters.", p))
		}
		if skipped(clean) {
			continue
		}
		if _, dup := files[clean]; dup {
			return 0, web.Refuse(http.StatusBadRequest, "duplicate_path", fmt.Sprintf("The path %q is sent twice.", clean))
		}
		files[clean] = b
		total += int64(len(b))
	}
	kept := map[string]string{} // path in the site -> live file to link
	if keep && site != nil && site.LiveVersion > 0 {
		live := h.a.Sites.VersionDir(site.ID, site.LiveVersion)
		err := filepath.WalkDir(live, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			rel, _ := filepath.Rel(live, p)
			if _, sent := files[filepath.ToSlash(rel)]; !sent {
				info, err := d.Info()
				if err != nil {
					return err
				}
				kept[filepath.ToSlash(rel)] = p
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	if len(files)+len(kept) == 0 {
		return 0, web.Refuse(http.StatusBadRequest, "no_files", "Send at least one file, such as index.html.")
	}
	if err := limits.Fits(r, limits.SiteBytes, total); err != nil {
		return 0, err
	}
	if b, ok := files["_redirects"]; ok {
		if _, mistakes := sites.ParseRedirects(string(b)); len(mistakes) > 0 {
			return 0, web.Refuse(http.StatusBadRequest, "bad_redirects", "Fix _redirects: "+strings.Join(mistakes, "; ")+".")
		}
	}
	if site == nil {
		var err error
		if site, err = h.a.Sites.Create(r.Context(), ownerID, r.PathValue("site")); err != nil {
			return 0, err
		}
	}
	dir, err := h.a.Sites.Stage(site)
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir) // gone already once committed
	for rel, from := range kept {
		to := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return 0, err
		}
		if err := os.Link(from, to); err != nil { // versions never change, so a link is a copy
			return 0, err
		}
	}
	for rel, b := range files {
		to := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(to, b, 0o644); err != nil {
			return 0, err
		}
	}
	return h.a.Sites.Commit(r.Context(), site, dir, web.CallerOf(r).ID)
}

// safePath is a relative path inside the site, or false (copied rules from
// the old host/tarball: no .., no control characters, at most 1024 bytes
// and 32 levels).
func safePath(name string) (string, bool) {
	name = strings.ReplaceAll(name, `\`, "/")
	for strings.HasPrefix(name, "./") || strings.HasPrefix(name, "/") {
		name = strings.TrimPrefix(strings.TrimPrefix(name, "./"), "/")
	}
	if name == "" || len(name) > 1024 || strings.ContainsFunc(name, func(c rune) bool { return c < 0x20 }) {
		return "", false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", false
		}
	}
	clean := path.Clean(name)
	return clean, clean != "." && strings.Count(clean, "/") < 32
}

// skipped: junk and credential files are left out of every site (copied
// from the old host/tarball), so publishing a folder never leaks a secret.
func skipped(clean string) bool {
	parts := strings.Split(clean, "/")
	for _, dir := range parts[:len(parts)-1] {
		switch dir {
		case ".git", ".hg", ".svn", ".bzr", ".ssh":
			return true
		}
	}
	base := parts[len(parts)-1]
	switch base {
	case ".DS_Store", ".htpasswd", ".htaccess", ".npmrc", ".netrc", "id_rsa", "id_ed25519", ".git-credentials", ".env":
		return true
	case ".env.example", ".env.sample", ".env.template":
		return false
	}
	return strings.HasPrefix(base, "._") || strings.HasPrefix(base, ".env.")
}
