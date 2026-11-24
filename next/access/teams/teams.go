// Package teams is teams (F5). A team is an owner like a person: it has a
// handle and sites, and every member manages them with their own sign-in.
// Nobody is a member until they accept, and an invitation reaches only the
// account with that email.
package teams

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/db"
	"simplehost/core/web"
)

type handler struct{ a *app.App }

// Mount adds routes 16 to 20.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("POST /v1/teams", web.Func(h.create))
	a.Routes.Platform.Handle("POST /v1/teams/{team}/invites", web.Func(h.invite))
	a.Routes.Platform.Handle("POST /v1/invites/{id}/accept", web.Func(h.accept))
	a.Routes.Platform.Handle("DELETE /v1/teams/{team}/members/{person}", web.Func(h.remove))
	a.Routes.Platform.Handle("DELETE /v1/teams/{team}", web.Func(h.delete))
}

var errSignIn = web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in, or send your key as Authorization: Bearer <key>.")

// team is the {team} the caller is a member of.
func (h *handler) team(r *http.Request) (access.Person, error) {
	c := web.CallerOf(r)
	if c.ID == "" {
		return access.Person{}, errSignIn
	}
	t, err := h.a.People.ByHandle(r.Context(), r.PathValue("team"))
	if err != nil || !t.IsTeam {
		return t, web.NotFound
	}
	ok, err := h.a.People.ActsFor(r.Context(), c, t.ID)
	if err == nil && !ok {
		err = web.Refuse(http.StatusForbidden, "not_member", "Only members of this team can do this.")
	}
	return t, err
}

// create is route 16: a team with its own handle, the caller its first member.
func (h *handler) create(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return errSignIn
	}
	var in struct {
		Handle      string
		DisplayName string `json:"display_name"`
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	handle := strings.ToLower(strings.TrimSpace(in.Handle))
	ctx := r.Context()
	tx, err := h.a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id, err := access.NewTeam(ctx, tx, strings.TrimSpace(in.DisplayName))
	if err != nil {
		return err
	}
	if err := access.ClaimName(ctx, tx, handle, "handle", id, ""); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, person_id) VALUES ($1, $2)`, id, c.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	h.a.Sites.Forget()
	web.JSON(w, http.StatusCreated, map[string]string{"id": id, "handle": handle})
	return nil
}

// invite is route 17: an emailed invitation. It makes nobody a member.
func (h *handler) invite(w http.ResponseWriter, r *http.Request) error {
	t, err := h.team(r)
	if err != nil {
		return err
	}
	var in struct{ Email string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(email, "@") {
		return web.Refuse(http.StatusBadRequest, "bad_email", "Give a full email address, like name@example.com.")
	}
	var id string
	err = h.a.DB.QueryRowContext(r.Context(), `INSERT INTO team_invites (team_id, email, invited_by) VALUES ($1, $2, $3) RETURNING id`,
		t.ID, email, web.CallerOf(r).ID).Scan(&id)
	if err != nil {
		return err
	}
	text := web.CallerOf(r).Email + " invited you to the team " + t.Handle + " on Simple Host.\n\n" +
		"To accept, sign in at https://" + h.a.Config.AppDomain + "/dashboard with this email address."
	if err := h.a.Mail.Send(r.Context(), email, "You are invited to the team "+t.Handle+" on Simple Host", text); err != nil {
		return err
	}
	web.JSON(w, http.StatusCreated, map[string]string{"id": id})
	return nil
}

// accept is route 18: only the account with the invited email can accept.
func (h *handler) accept(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return errSignIn
	}
	ctx := r.Context()
	tx, err := h.a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var teamID, email string
	err = tx.QueryRowContext(ctx, `SELECT team_id, email FROM team_invites WHERE id::text = $1 FOR UPDATE`, r.PathValue("id")).Scan(&teamID, &email)
	if err == sql.ErrNoRows {
		return web.NotFound
	}
	if err != nil {
		return err
	}
	if !strings.EqualFold(email, c.Email) {
		return web.Refuse(http.StatusForbidden, "invite_for_another_email", "This invitation was sent to another email address. Sign in with that address to accept it.")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO team_members (team_id, person_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, teamID, c.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE team_invites SET accepted_at = now() WHERE id::text = $1`, r.PathValue("id")); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]string{"team_id": teamID})
	return nil
}

// remove is route 19: remove a member, or leave. The last member cannot
// leave: delete the team instead.
func (h *handler) remove(w http.ResponseWriter, r *http.Request) error {
	t, err := h.team(r)
	if err != nil {
		return err
	}
	ctx := r.Context()
	tx, err := h.a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = $1 AND person_id::text = $2`, t.ID, r.PathValue("person"))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.NotFound
	}
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM team_members WHERE team_id = $1`, t.ID).Scan(&left); err != nil {
		return err
	}
	if left == 0 {
		return web.Refuse(http.StatusConflict, "last_member", "A team needs one member. Delete the team instead.")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]bool{"removed": true})
	return nil
}

// delete is route 20: a team with no sites. Its handle is retired for good.
func (h *handler) delete(w http.ResponseWriter, r *http.Request) error {
	t, err := h.team(r)
	if err != nil {
		return err
	}
	n, err := h.a.Sites.Count(r.Context(), t.ID)
	if err != nil {
		return err
	}
	if n > 0 {
		return web.Refuse(http.StatusConflict, "team_has_sites", "Delete the team's sites first.")
	}
	if err := access.Remove(r.Context(), h.a.DB, t.ID); err != nil {
		return err
	}
	h.a.Sites.Forget()
	web.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
	return nil
}

// Team is a team the person is in, or has been invited to (Invite set).
type Team struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Invite      string `json:"invite_id,omitempty"`
}

// List is the person's teams and open invitations (for GET /v1/me).
func List(ctx context.Context, q db.Q, personID, email string) ([]Team, error) {
	rows, err := q.QueryContext(ctx, `SELECT p.id, COALESCE(n.name, ''), p.display_name, '' FROM team_members m JOIN people p ON p.id = m.team_id
		LEFT JOIN names n ON n.person_id = p.id AND n.kind = 'handle' AND n.retired_at IS NULL WHERE m.person_id = $1
		UNION ALL
		SELECT p.id, COALESCE(n.name, ''), p.display_name, i.id::text FROM team_invites i JOIN people p ON p.id = i.team_id
		LEFT JOIN names n ON n.person_id = p.id AND n.kind = 'handle' AND n.retired_at IS NULL
		WHERE i.email = $2 AND i.accepted_at IS NULL AND NOT EXISTS (SELECT 1 FROM team_members m WHERE m.team_id = i.team_id AND m.person_id = $1)`,
		personID, strings.ToLower(email))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Handle, &t.DisplayName, &t.Invite); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Member is one member of a team.
type Member struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Members is a team's members and the emails invited but not yet joined
// (for GET /v1/me).
func Members(ctx context.Context, q db.Q, teamID string) (members []Member, invited []string, err error) {
	rows, err := q.QueryContext(ctx, `SELECT p.id, p.email FROM team_members m JOIN people p ON p.id = m.person_id WHERE m.team_id = $1 ORDER BY p.email`, teamID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	members = []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Email); err != nil {
			return nil, nil, err
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	inv, err := q.QueryContext(ctx, `SELECT email FROM team_invites WHERE team_id = $1 AND accepted_at IS NULL ORDER BY email`, teamID)
	if err != nil {
		return nil, nil, err
	}
	defer inv.Close()
	invited = []string{}
	for inv.Next() {
		var e string
		if err := inv.Scan(&e); err != nil {
			return nil, nil, err
		}
		invited = append(invited, e)
	}
	return members, invited, inv.Err()
}
