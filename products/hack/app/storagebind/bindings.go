// Package storagebind supplies Hack's trusted facts to storage row rules.
package storagebind

import (
	"context"
	"database/sql"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type Provider struct {
	DB *sql.DB
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
	b, err := hackevents.ReadStorageBindings(ctx, p.DB, c.SiteID, c.UserID)
	if err != nil {
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

func (p Provider) OwnerOnSite(ctx context.Context, siteID, userID string) (bool, error) {
	return hackevents.HackSiteOwnerOnSite(ctx, p.DB, siteID, userID)
}
