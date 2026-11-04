package operations

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SignupGroup is one row of the admin page's "Sign-ups by source". Where, when
// set, follows the label in the Overview ("Vercel Ship (ship.vineetsriram.com)").
type SignupGroup struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Where string `json:"where,omitempty"`
}

var (
	reChatGPT = regexp.MustCompile(`(?i)chatgpt|openai`)
	reClaude  = regexp.MustCompile(`(?i)claude|anthropic`)
	reGrok    = regexp.MustCompile(`(?i)grok|xai|x\.ai`)
)

// SignupGroupOf is the group key for a signup_source.
func SignupGroupOf(source string, arrivalSources map[string]SignupGroup) string {
	switch {
	case source == "":
		return "unknown"
	case source == "visitor_sso", source == model.SignupWebsite, source == model.SignupAgent, source == model.SignupVisitor, source == model.SignupAdmin, source == model.SignupReviewer:
		return source
	case strings.HasPrefix(source, model.SignupFromPrefix):
		return source
	case strings.HasPrefix(source, model.SignupConnectorPrefix):
		name := strings.TrimPrefix(source, model.SignupConnectorPrefix)
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
var fixedGroups = []SignupGroup{
	{Key: "c-ChatGPT", Label: "ChatGPT"}, {Key: "c-Claude", Label: "Claude"}, {Key: "c-Grok", Label: "Grok"},
	{Key: "c-other", Label: "Other connector"}, {Key: "agent", Label: "AI agent", Where: "skill or API"},
	{Key: "visitor_sso", Label: "Visitor via SSO"},
	{Key: "visitor", Label: "Site visitor sign-in"}, {Key: "admin", Label: "Issued by admin"},
	{Key: "reviewer", Label: "App reviewer"}, {Key: "other", Label: "Other"}, {Key: "unknown", Label: "Unknown (before tracking)"},
}

// SignupGroups lists every group in display order: the website, the named
// outside sources, any other from value someone signed up through, then the
// rest. present holds the group keys in use, for the unnamed from values.
func SignupGroups(present map[string]bool, arrivalSources map[string]SignupGroup) []SignupGroup {
	out := []SignupGroup{{Key: model.SignupWebsite, Label: "Website"}}
	named := make([]string, 0, len(arrivalSources))
	for k := range arrivalSources {
		named = append(named, k)
	}
	sort.Strings(named)
	for _, k := range named {
		out = append(out, SignupGroup{Key: k, Label: arrivalSources[k].Label, Where: arrivalSources[k].Where})
	}
	var other []string
	for k := range present {
		if strings.HasPrefix(k, model.SignupFromPrefix) {
			other = append(other, k)
		}
	}
	sort.Strings(other)
	for _, k := range other {
		out = append(out, SignupGroup{Key: k, Label: "from=" + strings.TrimPrefix(k, model.SignupFromPrefix), Where: "link"})
	}
	return append(out, fixedGroups...)
}

// CohortAccount is one account in a group's drill-down.
type CohortAccount struct {
	ID             string               `json:"id"`
	Email          string               `json:"email"`
	Handle         string               `json:"handle"`
	CreatedAt      time.Time            `json:"created_at"`
	Source         string               `json:"signup_source"`
	Method         string               `json:"signup_method"`
	Agent          string               `json:"signup_agent"`
	Detail         *model.SignupDetail  `json:"signup_detail"`
	Apps           []model.ConnectedApp `json:"connected_apps"`
	FirstConnectAt *time.Time           `json:"first_connect_at"`
	SitesPublished int                  `json:"sites_published"`
	FirstPublishAt *time.Time           `json:"first_publish_at"`
	Visits         int64                `json:"visits"`
	LastActiveAt   *time.Time           `json:"last_active_at"`
}
type CohortFunnel struct {
	SignedUp  int `json:"signed_up"`
	Connected int `json:"connected_ai"`
	Published int `json:"published"`
	Visited   int `json:"got_visitors"`
}

// cohort loads the accounts in one group, newest first, with the funnel.
func (h *Reporting) Cohort(ctx context.Context, group string, arrivalSources map[string]SignupGroup) ([]CohortAccount, CohortFunnel, []SignupGroup, error) {
	users, err := accounts.ListSiteOwners(ctx, h.database)
	if err != nil {
		return nil, CohortFunnel{}, nil, err
	}
	present := map[string]bool{}
	var in []model.User
	for _, u := range users {
		if u.IsAdmin {
			continue
		}
		g := SignupGroupOf(u.Signup.Source, arrivalSources)
		present[g] = true
		if g == group {
			in = append(in, u)
		}
	}
	ids := make([]string, len(in))
	for i, u := range in {
		ids[i] = u.ID
	}
	acts, err := accounts.ActivationFor(ctx, h.database, ids)
	if err != nil {
		return nil, CohortFunnel{}, nil, err
	}
	var f CohortFunnel
	out := make([]CohortAccount, 0, len(in))
	for i := len(in) - 1; i >= 0; i-- {
		u, a := in[i], acts[in[i].ID]
		c := CohortAccount{ID: u.ID, Handle: u.Handle.String, CreatedAt: u.CreatedAt,
			Source: u.Signup.Source, Method: u.Signup.Method, Agent: u.Signup.Agent, Detail: u.Signup.Detail,
			Apps: a.Apps, SitesPublished: a.SitesPublished, Visits: a.Visits,
			FirstPublishAt: timePtr(a.FirstPublish), LastActiveAt: timePtr(a.LastActive)}
		if strings.Contains(u.Username, "@") {
			c.Email = u.Username
		}
		// Signed up on the consent page but disconnected since: the app was
		// still connected, at sign-up.
		if app := signupApp(u.Signup); app != "" && !hasApp(c.Apps, app) {
			c.Apps = append([]model.ConnectedApp{{Name: app, First: u.CreatedAt}}, c.Apps...)
		}
		if c.Apps == nil {
			c.Apps = []model.ConnectedApp{}
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
	return out, f, SignupGroups(present, arrivalSources), nil
}

// signupApp is the app an account was created through on the consent page.
func signupApp(s model.Signup) string {
	if strings.HasPrefix(s.Source, model.SignupConnectorPrefix) {
		return strings.TrimPrefix(s.Source, model.SignupConnectorPrefix)
	}
	if s.Detail != nil && s.Agent != "" { // credited to a link; Agent is the app
		return s.Agent
	}
	return ""
}
func hasApp(apps []model.ConnectedApp, name string) bool {
	for _, a := range apps {
		if a.Name == name {
			return true
		}
	}
	return false
}

func timePtr(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
