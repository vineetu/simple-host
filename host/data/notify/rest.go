package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	repo "github.com/vsriram/simple-host/host/data/repo/notify"

	"github.com/vsriram/simple-host/host/access"
	datarepo "github.com/vsriram/simple-host/host/data/repo"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// Error is a refusal a caller answers with its code ("" Message: the code
// in words).
type Error struct{ Code, Message string }

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return strings.ReplaceAll(e.Code, "_", " ")
}

func refusal(code string) error { return &Error{Code: code} }

var (
	errNotEnabled = &Error{Code: "notifications_not_enabled", Message: "The server has no PASSCODE_ENC_KEY; new notifications are unavailable."}
	errBadHeader  = &Error{Code: "invalid_header", Message: "Use Authorization or X- followed by 1 to 62 letters, digits, or hyphens; X-Forwarded-*, X-Real-IP, X-MCP-*, X-Client-IP, and X-Originating-IP are reserved. Values must be printable ASCII, at most 1024 bytes."}
)

// Requester is who a notification request is for: Person is the signed-in
// person (also when acting for an account), Account the account whose
// sites the request names.
type Requester struct {
	Person, Account string
	// DeployOnly: a deploy-only key, which never manages notifications.
	DeployOnly bool
	// Member: acting for an account they belong to (a team, or a site
	// they edit).
	Member bool
}

// Subject is a resolved notification request: the site it names and the
// subscription it names, if any.
type Subject struct {
	Requester
	Site         model.Site
	Notification model.Notification
}

// refuse is the one "who may subscribe" rule for REST and MCP: a person's
// own key or sign-in (never a deploy-only key), on an active account that
// is not a hackathon's (not_offered).
func (e *Engine) refuse(ctx context.Context, person string, deployOnly bool) error {
	if person == "" || deployOnly {
		return refusal("forbidden")
	}
	if user, err := access.New(e.db).GetUserByID(ctx, person); err != nil || user.Suspended {
		return refusal("forbidden")
	}
	event, err := accounts.EventAccountFlag(ctx, e.db, person)
	if err != nil {
		return err
	}
	if event {
		return refusal("not_offered")
	}
	return nil
}

// Resolve checks who is asking and loads the site (by name, on the
// requester's account) and the subscription (by id, theirs on that site)
// the request names.
func (e *Engine) Resolve(ctx context.Context, who Requester, siteName, id string) (Subject, error) {
	s := Subject{Requester: who}
	if err := e.refuse(ctx, who.Person, who.DeployOnly); err != nil {
		return s, err
	}
	if siteName != "" {
		site, err := sitesrepo.GetSiteByUser(ctx, e.db, who.Account, siteName)
		if err != nil {
			return s, refusal("not_found")
		}
		s.Site = site
	}
	if id == "" {
		return s, nil
	}
	n, err := repo.GetNotification(ctx, e.db, id)
	if err != nil || n.OwnerID != who.Person || n.SiteID != s.Site.ID {
		return s, refusal("not_found")
	}
	s.Notification = n
	return s, nil
}

// Limits are the notification limits the dashboard shows.
func (e *Engine) Limits(env Env) map[string]any {
	l := e.limits
	return map[string]any{"enabled": env.Ready(), "per_site": l.NotifyMaxPerSite, "per_minute": l.NotifyPerMinute, "per_day": l.NotifyPerDay, "history_days": l.NotifyHistoryDays}
}

// View is a subscription as the REST API shows it.
func View(n model.Notification) map[string]any {
	state := n.State
	if n.ExpiresAt != nil && !n.ExpiresAt.After(time.Now()) {
		state = "expired"
	} else if n.Source == "mcp" && n.VerifiedAt == nil {
		state = "pending"
	}
	kind := n.Kind
	if kind == "" {
		kind = "webhook"
	}
	out := map[string]any{"id": n.ID, "site": n.Site, "url": n.URL, "events": n.Events, "storage": n.Storage, "label": n.Label, "source": n.Source, "app_name": n.AppName, "state": state, "created_at": n.CreatedAt, "expires_at": n.ExpiresAt, "last_attempt_at": n.LastAttemptAt, "last_success_at": n.LastSuccessAt, "last_result": n.LastResult, "failing_since": n.FailingSince, "auth_header_name": n.AuthHeaderName, "kind": kind}
	if kind == "web-push" {
		out["subscriber_role"] = n.SubscriberRole
		out["device_label"] = n.DeviceLabel
		out["muted_resources"] = n.MutedResources
	}
	return out
}

// List answers GET .../notifications: who's subscriptions on siteName
// ("": on every site) as REST views, with the limits the dashboard shows.
func (e *Engine) List(ctx context.Context, env Env, who Requester, siteName string) (map[string]any, error) {
	views, err := e.views(ctx, who, siteName)
	if err != nil {
		return nil, err
	}
	return map[string]any{"notifications": views, "limits": e.Limits(env)}, nil
}

// views is who's subscriptions on siteName as REST views. A member's site
// not published yet has none.
func (e *Engine) views(ctx context.Context, who Requester, siteName string) ([]map[string]any, error) {
	out := []map[string]any{}
	if err := e.refuse(ctx, who.Person, who.DeployOnly); err != nil {
		return out, err
	}
	var site model.Site
	if siteName != "" {
		var err error
		site, err = sitesrepo.GetSiteByUser(ctx, e.db, who.Account, siteName)
		if errors.Is(err, sql.ErrNoRows) && who.Member {
			return out, nil
		}
		if err != nil {
			return out, refusal("not_found")
		}
	}
	ns, err := repo.ListNotifications(ctx, e.db, who.Person, site.ID)
	for _, n := range ns {
		out = append(out, View(n))
	}
	return out, err
}

// eventNames are the events a subscription may name: Simple Hack offers
// the record events only for now.
func (e *Engine) eventNames() []string {
	if e.ed.Notifications().EventScoped {
		return RecordEventNames
	}
	return append(slices.Clone(RecordEventNames), SiteEventNames...)
}

// validEvents: one or more known events, none twice.
func (e *Engine) validEvents(events []string) bool {
	names := e.eventNames()
	if len(events) < 1 || len(events) > len(names) {
		return false
	}
	seen := map[string]bool{}
	for _, name := range events {
		if !slices.Contains(names, name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

var headerName = regexp.MustCompile(`^[Xx]-[A-Za-z0-9-]{1,62}$`)

// validHeader: no header, or Authorization or an X- header that is not a
// forwarding or MCP one, with a printable ASCII value of at most 1024 bytes.
func validHeader(name, value string) bool {
	if name == "" {
		return value == ""
	}
	lower := strings.ToLower(name)
	if lower != "authorization" && !headerName.MatchString(name) || len(value) > 1024 {
		return false
	}
	if strings.HasPrefix(lower, "x-forwarded-") || strings.HasPrefix(lower, "x-mcp-") || slices.Contains([]string{"x-real-ip", "x-client-ip", "x-originating-ip"}, lower) {
		return false
	}
	for _, c := range value {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

// resourceExists reports whether name ("" always) is a storage resource
// of siteID.
func (e *Engine) resourceExists(ctx context.Context, siteID, name string) bool {
	if name == "" {
		return true
	}
	_, err := datarepo.LoadResource(ctx, e.db, siteID, name)
	return err == nil
}

// checkURL validates a receiver: invalid_url, or refused_address when it
// resolves to a non-public address.
func (e *Engine) checkURL(ctx context.Context, raw string) error {
	_, err := e.client.validate(ctx, raw)
	if errors.Is(err, Failure("refused_address")) {
		return refusal("refused_address")
	}
	if err != nil {
		return refusal("invalid_url")
	}
	return nil
}

// CreateInput is an owner's new webhook subscription.
type CreateInput struct {
	URL, Storage, Secret, Label string
	Events                      []string
	HeaderName, HeaderValue     string
}

// Create validates and saves a webhook subscription on the subject's site.
// It returns the saved subscription as the REST API shows it (View) and its
// signing secret (generated when the input has none).
func (e *Engine) Create(ctx context.Context, env Env, s Subject, in CreateInput) (map[string]any, string, error) {
	ctx, cancel := context.WithTimeout(ctx, e.limits.NotifyTimeout)
	defer cancel()
	if err := e.checkCreate(ctx, s, &in); err != nil {
		return nil, "", err
	}
	now := time.Now()
	n := model.Notification{ID: RandomID("sub_", 12), SiteID: s.Site.ID, OwnerID: s.Person, Product: s.Member, Source: "owner", URL: in.URL, Events: in.Events, Storage: in.Storage, Secret: in.Secret, Label: in.Label, AuthHeaderName: in.HeaderName, AuthHeaderValue: in.HeaderValue, VerifiedAt: &now}
	_, _, err := repo.SaveNotification(ctx, e.db, n, e.limits.NotifyMaxPerSite, nil, e.limits.NotifyHistoryDays, env.Secrets())
	switch {
	case errors.Is(err, model.ErrNotificationAccess):
		return nil, "", refusal("notification_access_ended")
	case errors.Is(err, model.ErrNotificationCap):
		return nil, "", refusal("too_many_notifications")
	case err != nil:
		return nil, "", err
	}
	if n, err = repo.GetNotification(ctx, e.db, n.ID); err != nil {
		return nil, "", err
	}
	return View(n), in.Secret, nil
}

// checkCreate validates a new subscription, in the order refusals are
// reported, and fills in a generated secret.
func (e *Engine) checkCreate(ctx context.Context, s Subject, in *CreateInput) error {
	switch {
	case !e.validEvents(in.Events):
		return refusal("invalid_events")
	case !validHeader(in.HeaderName, in.HeaderValue):
		return errBadHeader
	case !e.resourceExists(ctx, s.Site.ID, in.Storage):
		return refusal("storage_not_found")
	}
	if err := e.checkURL(ctx, in.URL); err != nil {
		return err
	}
	if in.Secret != "" {
		if _, err := SecretKey(in.Secret); err != nil {
			return refusal("invalid_secret")
		}
		return nil
	}
	var err error
	in.Secret, err = NewSecret()
	return err
}

// Pause pauses or resumes the subject's subscription and returns it as the
// REST API shows it (View).
func (e *Engine) Pause(ctx context.Context, s Subject, paused bool) (map[string]any, error) {
	if err := repo.PauseNotification(ctx, e.db, s.Notification.ID, paused); err != nil {
		return nil, err
	}
	n, err := repo.GetNotification(ctx, e.db, s.Notification.ID)
	if err != nil {
		return nil, err
	}
	e.signal()
	return View(n), nil
}

// Delete removes the subject's subscription.
func (e *Engine) Delete(ctx context.Context, s Subject) error {
	_, err := repo.DeleteNotification(ctx, e.db, s.Notification.ID)
	return err
}

// Rotate gives the subject's subscription a new signing secret; the old
// one keeps signing until the returned time. An MCP client's secret is
// its own (client_secret).
func (e *Engine) Rotate(ctx context.Context, env Env, s Subject) (string, *time.Time, error) {
	if !env.Ready() {
		return "", nil, errNotEnabled
	}
	if s.Notification.Source == "mcp" {
		return "", nil, refusal("client_secret")
	}
	secret, err := NewSecret()
	if err != nil {
		return "", nil, err
	}
	until, err := repo.RotateNotification(ctx, e.db, s.Notification.ID, secret, env.Secrets())
	return secret, until, err
}

// Deliveries is the subject's subscription's recent delivery history.
func (e *Engine) Deliveries(ctx context.Context, s Subject) ([]json.RawMessage, error) {
	return repo.NotificationHistory(ctx, e.db, s.Notification.ID, e.limits.NotifyHistoryDays)
}

// RequireReady refuses while new notifications are unavailable.
func RequireReady(env Env) error {
	if env.Ready() {
		return nil
	}
	return errNotEnabled
}
