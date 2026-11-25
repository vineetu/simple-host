// Package idle clears out abandoned sites: a site with no visits and no
// changes for 3 months gets one email to its owner. A visit or a change
// keeps it. Still quiet 30 days after the email, it moves to the bin (and
// is gone for good when its 7 days there run out). The same rule for every
// account; it is written on the privacy page.
package idle

import (
	"context"
	"log"
	"time"

	"simplehost/app"
)

// Quiet is how long without visits and changes makes a site abandoned;
// Grace is how long after the email it moves to the bin.
const (
	Quiet = "3 months"
	Grace = "30 days"
)

// Work sweeps once a day until ctx ends.
func Work(ctx context.Context, a *app.App) {
	for {
		if err := Sweep(ctx, a); err != nil && ctx.Err() == nil {
			log.Printf("idle sites: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

// quiet is the condition for a site with no visit and no change since t.
const quiet = `GREATEST(s.changed_at, COALESCE(s.last_visit_at, s.created_at)) < `

// Sweep runs the rule once: a site visited or changed since its email is
// kept (its clock starts again); a site warned more than Grace ago and still
// quiet goes to the bin; a newly quiet site's owner gets the one email.
func Sweep(ctx context.Context, a *app.App) error {
	if _, err := a.DB.ExecContext(ctx, `UPDATE sites s SET idle_warned_at = NULL
		WHERE idle_warned_at IS NOT NULL AND NOT (`+quiet+`s.idle_warned_at)`); err != nil {
		return err
	}
	gone, err := a.DB.QueryContext(ctx, `UPDATE sites s SET deleted_at = now() WHERE deleted_at IS NULL
		AND idle_warned_at < now() - interval '`+Grace+`' RETURNING s.owner_id, s.id`)
	if err != nil {
		return err
	}
	for gone.Next() {
		var owner, id string
		if gone.Scan(&owner, &id) == nil {
			a.Sites.Forget(owner, id)
		}
	}
	gone.Close()
	rows, err := a.DB.QueryContext(ctx, `UPDATE sites s SET idle_warned_at = now() WHERE deleted_at IS NULL AND idle_warned_at IS NULL
		AND `+quiet+`now() - interval '`+Quiet+`' RETURNING s.name, s.owner_id`)
	if err != nil {
		return err
	}
	type warn struct{ site, owner string }
	var warns []warn
	for rows.Next() {
		var w warn
		if err := rows.Scan(&w.site, &w.owner); err != nil {
			rows.Close()
			return err
		}
		warns = append(warns, w)
	}
	rows.Close()
	for _, w := range warns {
		to, err := a.DB.QueryContext(ctx, `SELECT email FROM people WHERE id = $1 AND email IS NOT NULL
			UNION SELECT p.email FROM team_members m JOIN people p ON p.id = m.person_id WHERE m.team_id = $1`, w.owner)
		if err != nil {
			return err
		}
		var emails []string
		for to.Next() {
			var e string
			to.Scan(&e)
			emails = append(emails, e)
		}
		to.Close()
		for _, e := range emails {
			if err := a.Mail.Send(ctx, e, "Your site "+w.site+" will be removed in 30 days",
				"Your site "+w.site+" has had no visits and no changes for 3 months.\n\n"+
					"To keep it, visit it or publish a change in the next 30 days. Otherwise it moves to the bin, where it stays 7 days before it is gone for good.\n\n"+
					"This is the abandoned-site rule on our privacy page."); err != nil {
				log.Printf("idle email for %s: %v", w.site, err)
			}
		}
	}
	return nil
}
