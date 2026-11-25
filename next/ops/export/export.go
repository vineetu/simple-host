// Package export is "download my data" (routes 9 and 10): a person asks, the
// download is queued and made one at a time, and they get an email with a
// link. It holds every site of theirs and of their teams (live files, saved
// data, stored files) and their account details, as one zip.
package export

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/web"
)

// Life is how long a ready download can be fetched.
const Life = 7 * 24 * time.Hour

// Mount adds routes 9 and 10.
func Mount(a *app.App) {
	a.Routes.Platform.Handle("POST /v1/me/export", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		c := web.CallerOf(r)
		if c.ID == "" || c.Visitor() {
			return web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in, or send your key as X-API-Key: <key>.")
		}
		if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO exports (person_id) SELECT $1
			WHERE NOT EXISTS (SELECT 1 FROM exports WHERE person_id = $1 AND status = 'queued')`, c.ID); err != nil {
			return err
		}
		web.JSON(w, http.StatusAccepted, map[string]string{"status": "queued", "says": "We are making it. You will get an email with a link when it is ready."})
		return nil
	}))
	a.Routes.Platform.Handle("GET /v1/exports/{token}", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		var id string
		err := a.DB.QueryRowContext(r.Context(), `SELECT id FROM exports WHERE token_hash = $1 AND status = 'ready' AND expires_at > now()`,
			access.Hash(r.PathValue("token"))).Scan(&id)
		if err == sql.ErrNoRows {
			return web.Refuse(http.StatusNotFound, "download_gone", "This download has run out or does not exist. Ask for a new one in the dashboard.")
		}
		if err != nil {
			return err
		}
		w.Header().Set("Content-Disposition", `attachment; filename="simple-host-data.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, file(a, id))
		return nil
	}))
}

func file(a *app.App, id string) string { return filepath.Join(a.Config.DataDir, "exports", id+".zip") }

// Work makes queued downloads one at a time, and removes ones that ran out.
func Work(ctx context.Context, a *app.App) {
	for {
		made, err := Next(ctx, a)
		if err != nil && ctx.Err() == nil {
			log.Printf("export: %v", err)
		}
		if made {
			continue
		}
		// A download that ran out goes, and so does a file whose row is gone
		// (it went with its account).
		a.DB.ExecContext(ctx, `DELETE FROM exports WHERE expires_at < now()`)
		zips, _ := filepath.Glob(file(a, "*"))
		for _, z := range zips {
			var kept bool
			if a.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM exports WHERE id::text = $1)`, strings.TrimSuffix(filepath.Base(z), ".zip")).Scan(&kept) == nil && !kept {
				os.Remove(z)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// Next makes the oldest queued download and emails its link. It reports
// whether there was one.
func Next(ctx context.Context, a *app.App) (bool, error) {
	var id, personID string
	err := a.DB.QueryRowContext(ctx, `SELECT id, person_id FROM exports WHERE status = 'queued' ORDER BY created_at LIMIT 1`).Scan(&id, &personID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	who, err := a.People.Get(ctx, personID)
	if err == nil {
		err = write(ctx, a, who, file(a, id))
	}
	if err != nil {
		a.DB.ExecContext(ctx, `UPDATE exports SET status = 'failed', expires_at = now() WHERE id = $1`, id)
		return true, err
	}
	token := access.NewSecret("ex_")
	if _, err := a.DB.ExecContext(ctx, `UPDATE exports SET status = 'ready', token_hash = $2, ready_at = now(), expires_at = $3 WHERE id = $1`,
		id, access.Hash(token), time.Now().Add(Life)); err != nil {
		return true, err
	}
	link := "https://" + a.Config.AppDomain + "/v1/exports/" + token
	return true, a.Mail.Send(ctx, who.Email, "Your Simple Host data is ready",
		"Your download is ready. It holds every site, its files and saved data, and your account details.\n\n"+link+"\n\nThe link works for 7 days.")
}

// write makes the zip: account.json, then per site
// sites/<owner>/<site>/{files,data,stored}/...
func write(ctx context.Context, a *app.App, who access.Person, path string) error {
	rows, err := a.DB.QueryContext(ctx, `SELECT s.id, COALESCE(n.name, ''), s.name, s.live_version, s.created_at FROM sites s
		LEFT JOIN names n ON n.person_id = s.owner_id AND n.kind = 'handle' AND n.retired_at IS NULL
		WHERE s.deleted_at IS NULL AND (s.owner_id = $1 OR s.owner_id IN (SELECT team_id FROM team_members WHERE person_id = $1)) ORDER BY 2, 3`, who.ID)
	if err != nil {
		return err
	}
	type site struct {
		ID      string    `json:"-"`
		Owner   string    `json:"owner"`
		Name    string    `json:"name"`
		Version int       `json:"live_version"`
		Created time.Time `json:"created_at"`
	}
	var list []site
	for rows.Next() {
		var s site
		if err := rows.Scan(&s.ID, &s.Owner, &s.Name, &s.Version, &s.Created); err != nil {
			rows.Close()
			return err
		}
		list = append(list, s)
	}
	rows.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	z := zip.NewWriter(f)
	account, _ := json.MarshalIndent(map[string]any{"id": who.ID, "email": who.Email, "handle": who.Handle,
		"display_name": who.DisplayName, "sites": list, "made_at": time.Now().UTC()}, "", "  ")
	w, err := z.Create("account.json")
	if err == nil {
		_, err = w.Write(account)
	}
	for _, s := range list {
		base := "sites/" + s.Owner + "/" + s.Name + "/"
		dirs := map[string]string{"files/": a.Sites.VersionDir(s.ID, s.Version),
			"data/": filepath.Join(a.Config.DataDir, "sites", s.ID, "data"), "stored/": filepath.Join(a.Config.DataDir, "sites", s.ID, "files")}
		for prefix, dir := range dirs {
			if err == nil {
				err = add(z, base+prefix, dir)
			}
		}
	}
	if err != nil {
		return err
	}
	return z.Close()
}

// add copies every file under dir into the zip under prefix (a missing dir
// adds nothing).
func add(z *zip.Writer, prefix, dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		w, err := z.Create(prefix + filepath.ToSlash(rel))
		if err == nil {
			_, err = io.Copy(w, in)
		}
		return err
	})
}
