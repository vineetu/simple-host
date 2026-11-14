package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/products/hack/app/teams"
)

type groupHosts struct {
	teams      *teams.Service
	page       func(http.ResponseWriter, *http.Request, model.User) bool
	screenshot func(http.ResponseWriter, *http.Request, model.User, string) bool
}

// NewGroupHosts supplies Hack's address lookups and built-in event pages.
func NewGroupHosts(db *sql.DB, page func(http.ResponseWriter, *http.Request, model.User) bool, screenshot func(http.ResponseWriter, *http.Request, model.User, string) bool) handler.GroupHosts {
	return &groupHosts{teams: teams.New(db), page: page, screenshot: screenshot}
}

func hostGroup(ev model.Event) handler.Group {
	return handler.Group{GroupRef: groupRef(ev), CustomWebsite: ev.WebsiteMode == "custom", TakenDown: ev.TakenDown()}
}
func (g *groupHosts) ByAccount(ctx context.Context, id string) (handler.Group, error) {
	ev, err := g.teams.SiteEventByAccount(ctx, id)
	return hostGroup(ev), err
}
func (g *groupHosts) BySlug(ctx context.Context, slug string) (handler.Group, error) {
	ev, err := g.teams.SiteEventBySlug(ctx, slug)
	return hostGroup(ev), err
}
func (g *groupHosts) HasSite(ctx context.Context, id, name string) (bool, error) {
	_, err := g.teams.TeamBySlug(ctx, id, name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
func (g *groupHosts) PinnedVersion(ctx context.Context, accountID, name string) (int, bool, error) {
	return g.teams.SitePinnedVersion(ctx, accountID, name)
}
func (g *groupHosts) ServePage(w http.ResponseWriter, r *http.Request, holder model.User) bool {
	if r.URL.Path == "/" && g.page != nil && g.page(w, r, holder) {
		return true
	}
	if slug, ok := hackScreenshotPath(r.URL.Path); ok && g.screenshot != nil {
		return g.screenshot(w, r, holder, slug)
	}
	return false
}
func (*groupHosts) Texts() handler.GroupTexts {
	return handler.GroupTexts{
		NotFoundTitle:   "Event not found",
		NotFoundBody:    "No event or page lives at this address.",
		NoPersonalSites: "team sites belong to an event: select your team in the Simple Hack connection before publishing",
	}
}
