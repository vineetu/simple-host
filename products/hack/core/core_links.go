package core

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	appsites "github.com/vsriram/simple-host/host/sites"

	"github.com/vsriram/simple-host/host/model"

	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// entrySiteState reads the team's site once for the entry view: whether it exists, its live version (nil when none), and whether it counts as the "site" rules field (a live version, not taken down: entries.TeamSiteLive).
func (h *Links) EntrySiteState(ctx context.Context, ev hackmodel.Event, teamSlug string, siteTakenDown bool) (exists bool, liveVersion any, live bool) {
	if h.Hooks == nil {
		return false, nil, false
	}
	info, _, err := h.Hooks.TeamSiteInfo(ctx, ev.AccountID, teamSlug)
	if err != nil || !info.Exists {
		return false, nil, false
	}
	if info.ActiveVersion > 0 {
		liveVersion = info.ActiveVersion
	}
	live = info.ActiveVersion > 0 && !info.Suspended && !siteTakenDown
	return true, liveVersion, live
}

// entrySiteLive is entrySiteState's third return, for callers (the sweep,
// the rules dry run) that only need whether the site counts as filled.
func (h *Links) EntrySiteLive(ctx context.Context, ev hackmodel.Event, teamSlug string, siteTakenDown bool) bool {
	_, _, live := h.EntrySiteState(ctx, ev, teamSlug, siteTakenDown)
	return live
}

func (h *Links) IconURL(ev hackmodel.Event) string {
	return h.IconURLSlug(ev.Slug)
}

func (h *Links) IconURLSlug(slug string) string {
	return strings.TrimRight(h.PublicBaseURL, "/") + "/v1/hack/events/" + slug + "/icon"
}

func (h *Links) teamSiteJSON(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam) map[string]any {
	var site *model.Site
	if h.Hooks != nil {
		info, st, err := h.Hooks.TeamSiteInfo(ctx, ev.AccountID, team.Slug)
		if err != nil {
			log.Printf("hack: team site %s/%s: %v", ev.Slug, team.Slug, err)
		} else if info.Exists {
			site = &st
		}
	}
	return h.TeamSiteJSONFrom(ev, team, site, h.TeamSitesReady(ev.Slug))
}

func (h *Links) TeamSiteJSONFrom(ev hackmodel.Event, team hackmodel.EventTeam, site *model.Site, ready bool) map[string]any {
	out := map[string]any{
		"name":              team.Slug,
		"url":               h.TeamSiteURL(ev.Slug, team.Slug),
		"ready":             ready,
		"exists":            false,
		"live_version":      nil,
		"taken_down":        team.SiteTakenDownAt.Valid,
		"taken_down_reason": team.SiteTakenDownReason,
	}
	if site != nil {
		out["exists"] = true
		out["live_version"] = site.ActiveVersion
		if site.Suspended() {
			out["taken_down"] = true
		}
	}
	return out
}

// teamList is what the organiser's and judges' lists read once per request
// instead of once per team.
type TeamList struct {
	States   map[string]hackmodel.TeamWriteState
	Members  map[string][]hackmodel.EventPerson // by team id
	Versions map[string]bool                    // "<site id>/<n>" of every stored version
	Sites    map[string]model.Site              // live sites of the holding account, by name
	Ready    bool
}

func (h *Links) LoadTeamList(ctx context.Context, ev hackmodel.Event) (TeamList, error) {
	h.PinDue(ctx, ev.ID)
	ready := h.TeamSitesReady(ev.Slug)
	data, err := h.Teams.LoadTeamList(ctx, ev)
	return TeamList{States: data.States, Members: data.Members, Versions: data.Versions, Sites: data.Sites, Ready: ready}, err

}

func (tl TeamList) Site(name string) *model.Site {
	if s, ok := tl.Sites[name]; ok {
		return &s
	}
	return nil
}

func (h *Hack) teamDeadlineJSON(ctx context.Context, team hackmodel.EventTeam, obj map[string]any) {
	obj["deadline"] = nil
	obj["frozen"] = false
	obj["extended"] = false
	obj["pinned_version"] = nil
	st, err := h.Teams.WriteState(ctx, team.EventID, team.ID)
	if err != nil {
		return
	}
	teamStateJSON(st, obj)
}

func teamStateJSON(st hackmodel.TeamWriteState, obj map[string]any) {
	obj["extended"] = st.HasOverride
	obj["deadline"] = RFC3339(st.Deadline)
	obj["frozen"] = st.Frozen()
	if st.PinnedVersion.Valid {
		obj["pinned_version"] = st.PinnedVersion.Int64
	}
}

// participantTeam loads the caller's team for a participant route: ok=false
// after writing the answer (409 no_team when on none).
func ParticipantTeam(w http.ResponseWriter, r *http.Request, svc *teams.Service, a Access) (hackmodel.EventTeam, bool) {
	team, err := svc.CallerTeam(r.Context(), a.Member)
	if err != nil {
		WriteError(w, err)
		return team, false
	}
	return team, true
}

func (h *Links) PinDue(ctx context.Context, eventID string) {
	if err := h.Teams.PinDue(ctx, eventID); err != nil {
		log.Printf("hack: pin %s: %v", eventID, err)
	}
}

// removeOrphanTeamSites moves the event's sites whose team is gone to
// Recently deleted (the hourly sweep catches anything missed here).
func (h *Links) RemoveOrphanTeamSites(ctx context.Context, ev hackmodel.Event) {
	h.Teams.RemoveOrphanTeamSites(ctx, ev, h.Trash())
}

func (h *Links) Trash() func(context.Context, string, string) error {
	if h.Hooks == nil {
		return nil
	}
	return h.Hooks.TrashTeamSite
}

func (h *Links) RequestEventCert(ev hackmodel.Event) {
	var request func(string)
	if h.Hooks != nil {
		request = h.Hooks.RequestSiteCert
	}
	h.Teams.RequestEventCert(ev, request)
}

// pinnedLink is the deadline version's preview link from a teamList: none
// for a site taken down (by the organiser or the platform) or gone.
func (h *Links) pinnedLink(ev hackmodel.Event, team hackmodel.EventTeam, tl TeamList, v int) (string, bool) {
	site := tl.Site(team.Slug)
	if h.Hooks == nil || !tl.Ready || site == nil || site.Suspended() || team.SiteTakenDownAt.Valid {
		return "", false
	}
	if st, ok := tl.States[team.ID]; ok && (st.SiteTakenDown || st.EventTakenDown) {
		return "", false
	}
	if !tl.Versions[site.ID+"/"+strconv.Itoa(v)] {
		return "", false
	}
	return h.Hooks.TeamPreviewLinkFor(*site, ev.Slug, v)
}

func (h *Links) RemoveEventFiles(ev hackmodel.Event) {
	if h.Hooks == nil {
		return
	}
	if err := h.Hooks.RemoveAccountFiles(ev.AccountID, ev.Slug); err != nil {
		log.Printf("hack: remove files of deleted event %s: %v", ev.Slug, err)
	}
}

// siteLinks are the instance's addresses and what Hack asks of the team-site
// side (hooks: the SiteHandler; nil in tests that do not need it, and team
// addresses then count as not ready).
type Links struct {
	Hooks         SiteHooks
	Teams         *teams.Service
	PublicBaseURL string
	SiteDomain    string
}

// teamSitesReady: the event's team addresses have their certificate.
func (h *Links) TeamSitesReady(eventSlug string) bool {
	return h.Hooks != nil && h.Hooks.TeamSitesReady(eventSlug)
}

// teamSiteFlatHostsOn: TEAM_SITE_FLAT_HOSTS is on (h.links.hooks, the SiteHandler,
// owns the config; false with no SiteHandler wired).
func (h *Links) TeamSiteFlatHostsOn() bool {
	return h.Hooks != nil && h.Hooks.TeamSiteFlatHostsOn()
}

// teamSiteURL is https://<team>.<event>.<SITE_DOMAIN>/, or the flat
// <team>--<event>.<SITE_DOMAIN> form under TEAM_SITE_FLAT_HOSTS — h.links.hooks
// (the SiteHandler) owns that config, through the one function
// (appsites.TeamSiteHost) both packages call, so the format never scatters.
func (h *Links) TeamSiteURL(eventSlug, teamSlug string) string {
	if h.Hooks != nil {
		return h.Hooks.TeamSiteURL(eventSlug, teamSlug)
	}
	// No SiteHandler wired (incomplete test/deployment wiring): there is no
	// TEAM_SITE_FLAT_HOSTS config to read here, so this always falls back to
	// the two-level form.
	return "https://" + appsites.TeamSiteHost(eventSlug, teamSlug, h.SiteDomain, false) + "/"
}

func (h *Links) EventURL(slug string) string {
	return "https://" + slug + "." + h.SiteDomain + "/"
}

func (h *Links) JoinURL(code string) string {
	return h.PublicBaseURL + "/join/" + code
}

func (h *Links) JudgeURL(code string) string {
	return h.PublicBaseURL + "/judge/" + code
}

// teamLinkURL is https://<hack host>/team/<code>: instant join, never event
// admission (checked at GET /v1/hack/team-links/{code} and at join).
func (h *Links) TeamLinkURL(code string) string {
	return h.PublicBaseURL + "/team/" + code
}

// teamCodeFromInput accepts either a bare team code or a pasted team link
// (https://<hack host>/team/<code>): when s parses as a URL on this host, or
// simply contains "/team/" (a bare path, or a link previewed/pasted before TLS
// terms are known), the last path segment is taken as the code.
func (h *Links) TeamCodeFromInput(s string) string {
	s = strings.TrimSpace(s)
	useLastSegment := strings.Contains(s, "/team/")
	if !useLastSegment {
		if u, err := url.Parse(s); err == nil && u.Host != "" && strings.EqualFold(u.Host, h.teamLinkHost()) {
			useLastSegment = true
		}
	}
	if useLastSegment {
		// Strip the query/fragment first, then trim trailing slashes: removing "?source=chat" from ".../abcd1234/?source=chat" leaves a trailing slash, which would make the last segment empty if not trimmed too.
		trimmed := s
		if i := strings.IndexAny(trimmed, "?#"); i >= 0 {
			trimmed = trimmed[:i]
		}
		trimmed = strings.TrimRight(trimmed, "/")
		if i := strings.LastIndex(trimmed, "/"); i >= 0 {
			s = trimmed[i+1:]
		} else {
			s = trimmed
		}
	}
	return events.NormalizeCode(s)
}

func (h *Links) teamLinkHost() string {
	if u, err := url.Parse(h.PublicBaseURL); err == nil {
		return u.Host
	}
	return ""
}
