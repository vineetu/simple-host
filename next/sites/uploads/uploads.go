// Package uploads is the one-time upload link (routes 27 to 29): one link
// for a big site sent as one archive, or for photos and files that go to a
// namespace's file store. An archive is published exactly as route 22
// publishes it (the request is handed to that route); photos and files go
// to the file store through data/core, the same path visitors' uploads take.
package uploads

import (
	"database/sql"
	_ "embed"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/web"
)

// LinkLife is how long an unused upload link works.
const LinkLife = 24 * time.Hour

//go:embed picker.html
var picker string

type handler struct{ a *app.App }

// Mount adds routes 27, 28 and 29.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("POST /v1/sites/{site}/upload-links", web.Func(h.link))
	a.Routes.Platform.Handle("GET /upload/{token}", web.Func(h.page))
	a.Routes.Platform.Handle("PUT /v1/uploads/{token}", web.Func(h.upload))
}

// link is route 27: {"kind": "archive"} or {"kind": "files", "namespace": "photos"}.
// The site must exist; publish it once first. A files link makes the namespace if new.
func (h *handler) link(w http.ResponseWriter, r *http.Request) error {
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	if err := h.a.Sites.Manage(r, site); err != nil {
		return err
	}
	var in struct{ Kind, Namespace string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	switch in.Kind {
	case "archive":
		in.Namespace = ""
	case "files":
		if err := h.a.Data.Create(r.Context(), site, in.Namespace); err != nil {
			return err
		}
	default:
		return web.Refuse(http.StatusBadRequest, "bad_kind", `Set "kind" to "archive" (a whole site) or "files" (photos and files, with a "namespace").`)
	}
	token := access.NewSecret("up_")
	expires := time.Now().Add(LinkLife)
	_, err = h.a.DB.ExecContext(r.Context(), `INSERT INTO upload_links (token_hash, site_id, person_id, kind, namespace, expires_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		access.Hash(token), site.ID, web.CallerOf(r).ID, in.Kind, in.Namespace, expires)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"url": "https://" + h.a.Config.AppDomain + "/upload/" + token,
		"put": "https://" + h.a.Config.AppDomain + "/v1/uploads/" + token, "kind": in.Kind, "namespace": in.Namespace, "expires_at": expires})
	return nil
}

type uploadLink struct{ siteID, personID, kind, namespace string }

func (h *handler) find(r *http.Request) (uploadLink, error) {
	var l uploadLink
	err := h.a.DB.QueryRowContext(r.Context(), `SELECT site_id, person_id, kind, namespace FROM upload_links
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`, access.Hash(r.PathValue("token"))).Scan(&l.siteID, &l.personID, &l.kind, &l.namespace)
	if err == sql.ErrNoRows {
		return l, web.Refuse(http.StatusNotFound, "link_used", "This upload link was used or has run out. Ask for a new one.")
	}
	return l, err
}

// page is route 28: the picker page behind a link. It shrinks photos in the
// browser before sending them.
func (h *handler) page(w http.ResponseWriter, r *http.Request) error {
	l, err := h.find(r)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	io.WriteString(w, strings.ReplaceAll(picker, "{{kind}}", l.kind))
	return nil
}

// upload is route 29. An archive goes to route 22 as the link's maker; files
// (multipart, one part per file) go to the namespace's file store. The link
// is spent once the upload succeeds.
func (h *handler) upload(w http.ResponseWriter, r *http.Request) error {
	l, err := h.find(r)
	if err != nil {
		return err
	}
	who, err := h.a.People.Get(r.Context(), l.personID)
	if err != nil {
		return err
	}
	if who.Suspended {
		return web.Refuse(http.StatusForbidden, "account_suspended", "This account is suspended. Write to support if you think this is a mistake.")
	}
	r = web.WithCaller(r, web.Caller{ID: who.ID, Email: who.Email, Operator: who.Operator, Via: "session"})
	site, err := h.a.Sites.ByID(r.Context(), l.siteID)
	if err != nil {
		return err
	}
	if err := h.a.Sites.Manage(r, site); err != nil {
		return err // no longer a member of the owning team
	}
	if l.kind == "archive" {
		to := r.Clone(r.Context())
		to.URL = &url.URL{Path: "/v1/sites/" + site.Name, RawQuery: url.Values{"owner": {site.OwnerHandle},
			"keep_other_files": {r.URL.Query().Get("keep_other_files")}}.Encode()}
		to.RequestURI = to.URL.RequestURI()
		rec := &status{ResponseWriter: w}
		h.a.Routes.Platform.ServeHTTP(rec, to)
		if rec.code == http.StatusOK {
			return h.spend(r)
		}
		return nil
	}
	ns, err := h.a.Data.Open(r, site, l.namespace)
	if err != nil {
		return err
	}
	defer ns.Close()
	parts, err := r.MultipartReader()
	if err != nil {
		return web.Refuse(http.StatusBadRequest, "bad_upload", "Send the files as multipart/form-data, one part per file.")
	}
	var names []string
	for {
		p, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return web.Refuse(http.StatusBadRequest, "bad_upload", "The upload was cut off. Try again.")
		}
		// The file name as sent, folders kept (p.FileName drops them).
		_, params, _ := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := params["filename"]
		if name == "" {
			continue
		}
		if err := h.a.Data.PutFile(r, ns, name, p.Header.Get("Content-Type"), who.ID, p); err != nil {
			return err
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return web.Refuse(http.StatusBadRequest, "no_files", "Choose at least one file.")
	}
	if err := h.spend(r); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"namespace": l.namespace, "files": names})
	return nil
}

func (h *handler) spend(r *http.Request) error {
	_, err := h.a.DB.ExecContext(r.Context(), `UPDATE upload_links SET used_at = now() WHERE token_hash = $1`, access.Hash(r.PathValue("token")))
	return err
}

// status remembers the status route 22 answered with.
type status struct {
	http.ResponseWriter
	code int
}

func (s *status) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}
