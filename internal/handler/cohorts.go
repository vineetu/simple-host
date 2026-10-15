package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Sign-ups by source (admin only, INTENT 2026-10-15): every account is put in
// one group from its signup_source, the Overview counts them, and a group
// opens to the accounts in it with what each did after joining.

// signupGroup is one row of the admin page's "Sign-ups by source". Where, when
// set, follows the label in the Overview ("Vercel Ship (ship.vineetsriram.com)").
type signupGroup struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Where string `json:"where,omitempty"`
}

var (
	reChatGPT = regexp.MustCompile(`(?i)chatgpt|openai`)
	reClaude  = regexp.MustCompile(`(?i)claude|anthropic`)
	reGrok    = regexp.MustCompile(`(?i)grok|xai|x\.ai`)
)

// signupGroupOf is the group key for a signup_source.
func signupGroupOf(source string) string {
	switch {
	case source == "":
		return "unknown"
	case source == db.SignupWebsite, source == db.SignupAgent, source == db.SignupVisitor, source == db.SignupAdmin, source == db.SignupReviewer:
		return source
	case strings.HasPrefix(source, db.SignupFromPrefix):
		return source
	case strings.HasPrefix(source, db.SignupConnectorPrefix):
		name := strings.TrimPrefix(source, db.SignupConnectorPrefix)
		switch {
		case reChatGPT.MatchString(name):
			return "c-ChatGPT"
		case reClaude.MatchString(name):
			return "c-Claude"
		case reGrok.MatchString(name):
			return "c-Grok"
		}
		return "c-other"
	}
	if _, ok := arrivalSources[source]; ok {
		return source
	}
	return "other"
}

// fixedGroups are the groups that are not outside links, in display order.
var fixedGroups = []signupGroup{
	{Key: "c-ChatGPT", Label: "ChatGPT"}, {Key: "c-Claude", Label: "Claude"}, {Key: "c-Grok", Label: "Grok"},
	{Key: "c-other", Label: "Other connector"}, {Key: "agent", Label: "AI agent", Where: "skill or API"},
	{Key: "visitor", Label: "Site visitor sign-in"}, {Key: "admin", Label: "Issued by admin"},
	{Key: "reviewer", Label: "App reviewer"}, {Key: "other", Label: "Other"}, {Key: "unknown", Label: "Unknown (before tracking)"},
}

// signupGroups lists every group in display order: the website, the named
// outside sources, any other from value someone signed up through, then the
// rest. present holds the group keys in use, for the unnamed from values.
func signupGroups(present map[string]bool) []signupGroup {
	out := []signupGroup{{Key: db.SignupWebsite, Label: "Website"}}
	named := make([]string, 0, len(arrivalSources))
	for k := range arrivalSources {
		named = append(named, k)
	}
	sort.Strings(named)
	for _, k := range named {
		out = append(out, signupGroup{Key: k, Label: arrivalSources[k].Label, Where: arrivalSources[k].Where})
	}
	var other []string
	for k := range present {
		if strings.HasPrefix(k, db.SignupFromPrefix) {
			other = append(other, k)
		}
	}
	sort.Strings(other)
	for _, k := range other {
		out = append(out, signupGroup{Key: k, Label: "from=" + strings.TrimPrefix(k, db.SignupFromPrefix), Where: "link"})
	}
	return append(out, fixedGroups...)
}

func groupLabel(groups []signupGroup, key string) string {
	for _, g := range groups {
		if g.Key == key {
			return g.Label
		}
	}
	return key
}

// cohortAccount is one account in a group's drill-down.
type cohortAccount struct {
	ID             string            `json:"id"`
	Email          string            `json:"email"`
	Handle         string            `json:"handle"`
	CreatedAt      time.Time         `json:"created_at"`
	Source         string            `json:"signup_source"`
	Method         string            `json:"signup_method"`
	Agent          string            `json:"signup_agent"`
	Detail         *db.SignupDetail  `json:"signup_detail"`
	Apps           []db.ConnectedApp `json:"connected_apps"`
	FirstConnectAt *time.Time        `json:"first_connect_at"`
	SitesPublished int               `json:"sites_published"`
	FirstPublishAt *time.Time        `json:"first_publish_at"`
	Visits         int64             `json:"visits"`
	LastActiveAt   *time.Time        `json:"last_active_at"`
}

type cohortFunnel struct {
	SignedUp  int `json:"signed_up"`
	Connected int `json:"connected_ai"`
	Published int `json:"published"`
	Visited   int `json:"got_visitors"`
}

// cohort loads the accounts in one group, newest first, with the funnel.
func (h *SiteHandler) cohort(r *http.Request, group string) ([]cohortAccount, cohortFunnel, []signupGroup, error) {
	users, err := db.ListAllUsers(r.Context(), h.database)
	if err != nil {
		return nil, cohortFunnel{}, nil, err
	}
	present := map[string]bool{}
	var in []db.User
	for _, u := range users {
		if u.IsAdmin {
			continue
		}
		g := signupGroupOf(u.Signup.Source)
		present[g] = true
		if g == group {
			in = append(in, u)
		}
	}
	ids := make([]string, len(in))
	for i, u := range in {
		ids[i] = u.ID
	}
	acts, err := db.ActivationFor(r.Context(), h.database, ids)
	if err != nil {
		return nil, cohortFunnel{}, nil, err
	}
	var f cohortFunnel
	out := make([]cohortAccount, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		u, a := in[i], acts[in[i].ID]
		c := cohortAccount{ID: u.ID, Handle: u.Handle.String, CreatedAt: u.CreatedAt,
			Source: u.Signup.Source, Method: u.Signup.Method, Agent: u.Signup.Agent, Detail: u.Signup.Detail,
			Apps: a.Apps, SitesPublished: a.SitesPublished, Visits: a.Visits,
			FirstPublishAt: timePtr(a.FirstPublish), LastActiveAt: timePtr(a.LastActive)}
		if strings.Contains(u.Username, "@") {
			c.Email = u.Username
		}
		// Signed up on the consent page but disconnected since: the app was
		// still connected, at sign-up.
		if app := signupApp(u.Signup); app != "" && !hasApp(c.Apps, app) {
			c.Apps = append([]db.ConnectedApp{{Name: app, First: u.CreatedAt}}, c.Apps...)
		}
		if c.Apps == nil {
			c.Apps = []db.ConnectedApp{}
		}
		for _, app := range c.Apps {
			if c.FirstConnectAt == nil || app.First.Before(*c.FirstConnectAt) {
				t := app.First
				c.FirstConnectAt = &t
			}
		}
		f.SignedUp++
		if len(c.Apps) > 0 {
			f.Connected++
		}
		if c.SitesPublished > 0 {
			f.Published++
		}
		if c.Visits > 0 {
			f.Visited++
		}
		out = append(out, c)
	}
	return out, f, signupGroups(present), nil
}

// signupApp is the app an account was created through on the consent page.
func signupApp(s db.Signup) string {
	if strings.HasPrefix(s.Source, db.SignupConnectorPrefix) {
		return strings.TrimPrefix(s.Source, db.SignupConnectorPrefix)
	}
	if s.Detail != nil && s.Agent != "" { // credited to a link; Agent is the app
		return s.Agent
	}
	return ""
}

func hasApp(apps []db.ConnectedApp, name string) bool {
	for _, a := range apps {
		if a.Name == name {
			return true
		}
	}
	return false
}

// requireAdmin answers 404 to anyone but the admin, like /v1/admin/users: the
// route does not confirm it exists to someone who may not use it.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return false
	}
	if !user.IsAdmin || user.Team != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return false
	}
	return true
}

// adminSignups is GET /v1/admin/signups?group=<key>: the accounts in one
// sign-up source group, what each did after joining, and the group's funnel.
func (h *SiteHandler) adminSignups(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "group is required"})
		return
	}
	accounts, funnel, groups, err := h.cohort(r, group)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"group": group, "label": groupLabel(groups, group), "funnel": funnel, "accounts": accounts})
}

// adminSignupsCSV is GET /v1/admin/signups.csv?group=<key>: the same accounts
// as a spreadsheet, every cell through csvSafe like the other exports.
func (h *SiteHandler) adminSignupsCSV(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "group is required"})
		return
	}
	accounts, _, groups, err := h.cohort(r, group)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	rows := [][]string{{"email", "handle", "signed_up_at", "source", "signup_method", "from", "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "referrer", "landed_at",
		"connected_ai_apps", "first_connect_at", "sites_published", "first_publish_at", "visits", "last_active_at"}}
	label := groupLabel(groups, group)
	for _, a := range accounts {
		d := a.Detail
		if d == nil {
			d = &db.SignupDetail{}
		}
		names := make([]string, len(a.Apps))
		for i, app := range a.Apps {
			names[i] = app.Name
		}
		landed := ""
		if !d.LandedAt.IsZero() {
			landed = rfc3339Time(d.LandedAt)
		}
		rows = append(rows, []string{a.Email, a.Handle, rfc3339Time(a.CreatedAt), label, a.Method, d.From, d.UTMSource, d.UTMMedium, d.UTMCampaign, d.UTMTerm, d.UTMContent, d.Referrer, landed,
			strings.Join(names, "; "), optTime(a.FirstConnectAt), strconv.Itoa(a.SitesPublished), optTime(a.FirstPublishAt), strconv.FormatInt(a.Visits, 10), optTime(a.LastActiveAt)})
	}
	slug := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
			return c
		}
		return '-'
	}, group)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="signups-%s-%s.csv"`, slug, time.Now().UTC().Format("2006-01-02")))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	for _, row := range rows {
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		if err := cw.Write(row); err != nil {
			return
		}
	}
}

func optTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return rfc3339Time(*t)
}
