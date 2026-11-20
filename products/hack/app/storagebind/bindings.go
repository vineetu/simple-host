// Package storagebind supplies Hack's trusted facts to storage row rules.
package storagebind

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/app/accounts"
	"github.com/vsriram/simple-host/host/app/sites"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type Provider struct {
	DB sqlkit.Querier
}

var _ appstorage.BindingProvider = Provider{}

func (Provider) Schema() ruleengine.BindingSchema {
	return ruleengine.BindingSchema{
		"auth.team":             {Type: ruleengine.TypeText, Nullable: true},
		"auth.teams":            {Type: ruleengine.TypeTextList},
		"auth.organiser":        {Type: ruleengine.TypeBool},
		"event.team":            {Type: ruleengine.TypeText, Nullable: true},
		"event.stage":           {Type: ruleengine.TypeText, Nullable: true},
		"event.before_deadline": {Type: ruleengine.TypeBool},
	}
}

func (p Provider) Values(ctx context.Context, c appstorage.BindingCall, needed []string) (ruleengine.Values, error) {
	var b hackevents.StorageBindings
	accountID, name, err := sites.SiteOwner(ctx, p.DB, c.SiteID)
	if err == nil {
		b, err = hackevents.ReadStorageBindings(ctx, p.DB, accountID, name, c.UserID)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	text := func(s sql.NullString) ruleengine.Value {
		if !s.Valid {
			return ruleengine.Null()
		}
		return ruleengine.Text(s.String)
	}
	teams := ruleengine.List()
	if b.Team.Valid {
		teams = ruleengine.List(b.Team.String)
	}
	all := ruleengine.Values{
		"auth.team":             text(b.Team),
		"auth.teams":            teams,
		"auth.organiser":        ruleengine.Bool(b.Organiser),
		"event.team":            text(b.SiteTeam),
		"event.stage":           text(b.Stage),
		"event.before_deadline": ruleengine.Bool(b.BeforeDeadline),
	}
	values := ruleengine.Values{}
	for _, name := range needed {
		if value, ok := all[name]; ok {
			values[name] = value
		}
	}
	return values, nil
}

// OwnerOnSite: the platform's answer (accounts.ActsAsOwner: an organiser is
// a member of the event's account, a teammate an editor of the team's site),
// narrowed by the event's own rules (hackevents.OwnerOnSite).
func (p Provider) OwnerOnSite(ctx context.Context, siteID, userID string) (bool, error) {
	accountID, name, err := sites.SiteOwner(ctx, p.DB, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ok, err := accounts.ActsAsOwner(ctx, p.DB, userID, accountID, name); err != nil || !ok {
		return false, err
	}
	if down, err := sites.SiteTakenDown(ctx, p.DB, siteID); err != nil || down {
		return false, err
	}
	return hackevents.OwnerOnSite(ctx, p.DB, accountID, name, userID)
}
