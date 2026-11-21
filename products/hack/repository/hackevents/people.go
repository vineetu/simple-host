package hackevents

import (
	"context"
	"strings"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	hostmodel "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// Simple Hack keeps no account fields of its own: names and sign-in
// addresses come from accounts (host/access) by id, one query per list.

// WithDisplayName fills a scanned member's display name from its account;
// sql.ErrNoRows when the account is gone.
func WithDisplayName(ctx context.Context, q sqlkit.Querier, m EventMember, err error) (EventMember, error) {
	if err != nil {
		return m, err
	}
	u, err := accounts.UserByID(ctx, q, m.UserID)
	if err != nil {
		return EventMember{}, err
	}
	m.DisplayName = u.DisplayName.String
	return m, nil
}

// WithAccounts fills each person's sign-in address and display name, leaving
// out anyone whose account is gone.
func WithAccounts(ctx context.Context, q sqlkit.Querier, people []EventPerson) ([]EventPerson, error) {
	ids := make([]string, len(people))
	for i, p := range people {
		ids[i] = p.UserID
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	var out []EventPerson
	for _, p := range people {
		if u, ok := users[p.UserID]; ok {
			p.Email, p.DisplayName = u.Username, u.DisplayName.String
			out = append(out, p)
		}
	}
	return out, nil
}

// hasAddress is whether the sign-in address can receive mail.
func hasAddress(u hostmodel.User) bool { return strings.Contains(u.Username, "@") }

// emailable is the one rule for who may be emailed, used both when a notice
// is queued and again at send time; updates is the person's email_updates.
func emailable(u hostmodel.User, updates bool) bool {
	return hasAddress(u) && !u.EventAccount && !u.Suspended && updates
}

// emailUpdatesByID reads email_updates for these people; a person with no
// preferences row is absent (on by default).
func emailUpdatesByID(ctx context.Context, q sqlkit.Querier, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := q.QueryContext(ctx, `SELECT user_id::text, COALESCE(email_updates, true) FROM hack_account_preferences WHERE user_id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var on bool
		if err := rows.Scan(&id, &on); err != nil {
			return nil, err
		}
		out[id] = on
	}
	return out, rows.Err()
}

// emailableIDs keeps the ids of the accounts that may be emailed.
func emailableIDs(ctx context.Context, q sqlkit.Querier, users map[string]hostmodel.User) ([]string, error) {
	ids := make([]string, 0, len(users))
	for id := range users {
		ids = append(ids, id)
	}
	prefs, err := emailUpdatesByID(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for id, u := range users {
		on, ok := prefs[id]
		if emailable(u, !ok || on) {
			out = append(out, id)
		}
	}
	return out, nil
}
