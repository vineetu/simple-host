package hackmembers

import (
	"context"
	"encoding/json"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	hostmodel "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// Simple Hack keeps no account fields of its own: names and sign-in
// addresses come from accounts (host/access) by id, one query per list.

type RegistrationApplication = model.RegistrationApplication

// People is everyone in the event, oldest member first, with their account's
// address and name (team slug and name are the team owner's to add).
func People(ctx context.Context, q sqlkit.Querier, f Filter) ([]EventPerson, error) {
	where, args := f.where()
	rows, err := q.QueryContext(ctx, `
		SELECT user_id, role, team_id, joined_at, coc_accepted_at, approval_status,
		       joined_via, city, country, age_range, gender, team_admin
		  FROM event_members WHERE `+where+` ORDER BY joined_at, user_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventPerson
	for rows.Next() {
		var p EventPerson
		if err := rows.Scan(&p.UserID, &p.Role, &p.TeamID, &p.JoinedAt, &p.CocAcceptedAt, &p.ApprovalStatus,
			&p.JoinedVia, &p.City, &p.Country, &p.AgeRange, &p.Gender, &p.Admin); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return WithAccounts(ctx, q, out)
}

// RegistrationApplications is every participant's application, oldest first.
func RegistrationApplications(ctx context.Context, q sqlkit.Querier, eventID string) ([]RegistrationApplication, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id,approval_status,signup_answers,joined_at,
		joined_via,city,country,age_range,gender
		FROM event_members WHERE event_id=$1 AND `+isParticipantSQL+` ORDER BY joined_at`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RegistrationApplication{}
	for rows.Next() {
		var a RegistrationApplication
		var raw []byte
		if err := rows.Scan(&a.UserID, &a.Status, &raw, &a.JoinedAt,
			&a.JoinedVia, &a.City, &a.Country, &a.AgeRange, &a.Gender); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &a.Answers); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, len(out))
	for i, a := range out {
		ids[i] = a.UserID
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	kept := []RegistrationApplication{}
	for _, a := range out {
		if u, ok := users[a.UserID]; ok {
			a.Email, a.DisplayName = u.Username, u.DisplayName.String
			kept = append(kept, a)
		}
	}
	return kept, nil
}

// emailable is the one rule for who may be emailed, used both when a notice
// is queued and again at send time; updates is the person's email_updates.
func emailable(u hostmodel.User, updates bool) bool {
	return hackevents.HasAddress(u) && !u.EventAccount && !u.Suspended && updates
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

// EmailUpdates reads the person's "Email me when something waits on me"
// setting (on by default) and whether their account has an address at all.
func EmailUpdates(ctx context.Context, q sqlkit.Querier, userID string) (on, possible bool, err error) {
	u, err := accounts.UserByID(ctx, q, userID)
	if err != nil {
		return false, false, err
	}
	prefs, err := emailUpdatesByID(ctx, q, []string{u.ID})
	if err != nil {
		return false, false, err
	}
	set, ok := prefs[u.ID]
	return !ok || set, hackevents.HasAddress(u) && !u.EventAccount, nil
}

type OtherOrganiser = model.OtherOrganiser

// OtherEventOrganisers is the event's organisers but userID, by display name.
func OtherEventOrganisers(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]OtherOrganiser, error) {
	ids, err := IDs(ctx, q, Filter{EventID: eventID, Role: "organiser", Except: userID})
	if err != nil {
		return nil, err
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	sorted, err := accounts.SortByDisplayName(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := []OtherOrganiser{}
	for _, id := range sorted {
		if u, ok := users[id]; ok {
			out = append(out, OtherOrganiser{UserID: id, DisplayName: u.DisplayName.String, Email: u.Username})
		}
	}
	return out, nil
}
