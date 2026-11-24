// Package erase is deleting an account (route 8): the person's sites with
// their files and saved data, keys, sign-ins and app connections go at once,
// and so does every team they are the only member of, with its sites. The
// handle is retired for good.
package erase

import (
	"net/http"
	"os"
	"path/filepath"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/web"
)

// Mount adds route 8.
func Mount(a *app.App) {
	a.Routes.Platform.Handle("DELETE /v1/me", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		return erase(w, r, a)
	}))
}

func erase(w http.ResponseWriter, r *http.Request, a *app.App) error {
	c := web.CallerOf(r)
	if c.ID == "" || c.Visitor() {
		return web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in to the dashboard, or send your key as Authorization: Bearer <key>.")
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The teams only this person is in go with them.
	owners := []string{c.ID}
	rows, err := tx.QueryContext(ctx, `SELECT team_id FROM team_members m WHERE person_id = $1
		AND NOT EXISTS (SELECT 1 FROM team_members o WHERE o.team_id = m.team_id AND o.person_id <> $1)`, c.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, id)
	}
	rows.Close()
	var siteIDs []string
	for _, owner := range owners {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM sites WHERE owner_id = $1`, owner)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			siteIDs = append(siteIDs, id)
		}
		rows.Close()
		// The person row's foreign keys take their sites, keys, sessions,
		// app connections and team rows with it.
		if err := access.Remove(ctx, tx, owner); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.Sites.Forget()
	for _, id := range siteIDs {
		os.RemoveAll(filepath.Join(a.Config.DataDir, "sites", id))
	}
	access.SetCookie(w, r, "sh_session", "", 0)
	web.JSON(w, http.StatusOK, map[string]any{"deleted": true, "sites": len(siteIDs)})
	return nil
}
