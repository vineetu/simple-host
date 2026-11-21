package notify

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

var errPushNotOffered = &Error{Code: "not_offered", Message: "web push is not enabled for this site"}

// pushOffered refuses unless siteID's account may use web push.
func (e *Engine) pushOffered(ctx context.Context, siteID string) error {
	allowed, err := e.PushAllowed(ctx, siteID)
	if err != nil {
		return err
	}
	if !allowed {
		return errPushNotOffered
	}
	return nil
}

// VAPIDPublicKey is the key a page's PushManager.subscribe() needs, for a
// site that may use web push.
func (e *Engine) VAPIDPublicKey(ctx context.Context, env Env, siteID string) (string, error) {
	if err := e.pushOffered(ctx, siteID); err != nil {
		return "", err
	}
	if err := RequireReady(env); err != nil {
		return "", err
	}
	pub, _, err := e.VAPIDKeys(ctx, env)
	if err == nil && pub == "" {
		err = errors.New("no VAPID public key")
	}
	return pub, err
}

// PushCaller is who calls a visitor-facing push route on a site.
type PushCaller struct {
	SiteID, SiteName, AccountID string
	// Owner: the site's owner by API key or account sign-in.
	Owner bool
	// VisitorID and VisitorFamily are the signed-in visitor and their
	// session's push-cleanup key (PushFamily); OnOwnAddress: the request
	// came on the site's own address (never framed, never a preview).
	VisitorID, VisitorFamily string
	OnOwnAddress             bool
}

// pushIdentity is the caller's push role ("owner" or "visitor") and the
// subscriber id it stores. The site's owner signed in on their own site's
// own address as a visitor gets owner rights here too, as for storage data.
// family is "" for an API key, which no sign-out reaches.
func (e *Engine) pushIdentity(ctx context.Context, c PushCaller) (role, subscriber, family string, err error) {
	if c.Owner {
		return "owner", c.AccountID, "", nil
	}
	if c.VisitorID == "" {
		return "", "", "", &Error{Code: "forbidden", Message: "sign in on this site first"}
	}
	if owner, err := access.ActsAsOwner(ctx, e.svc.database, c.VisitorID, c.AccountID, c.SiteName); err == nil && owner && c.OnOwnAddress {
		return "owner", c.AccountID, c.VisitorFamily, nil
	}
	return "visitor", c.VisitorID, c.VisitorFamily, nil
}

// PushSubscribable refuses a push subscribe on siteID unless its account
// may use web push, is not a team's (a team's site notifies nobody) and
// new notifications are available.
func (e *Engine) PushSubscribable(ctx context.Context, env Env, siteID, accountID string) error {
	if err := e.pushOffered(ctx, siteID); err != nil {
		return err
	}
	team, err := access.New(e.svc.database).IsTeam(ctx, accountID)
	if err != nil {
		return err
	}
	if team {
		return refusal("not_offered")
	}
	return RequireReady(env)
}

// PushSubscription is one browser's push subscription request.
type PushSubscription struct {
	Endpoint, P256dh, Auth string
	Resource, Label        string
	Events                 []string
}

// SubscribePush registers one device for the owner or a signed-in visitor
// (after PushSubscribable) and returns its id and the VAPID public key (""
// when it cannot be read). A visitor's device always gets exactly the
// record and file events: the fan-out's own visitor match keeps it to
// events about their own data; naming a resource only narrows it further.
// A repeated subscribe of the same browser subscription and resource
// updates its row instead of adding one.
func (e *Engine) SubscribePush(ctx context.Context, env Env, c PushCaller, in PushSubscription) (id, publicKey string, err error) {
	role, subscriber, family, err := e.pushIdentity(ctx, c)
	if err != nil {
		return "", "", err
	}
	events := in.Events
	resource := strings.TrimSpace(in.Resource)
	if role == "visitor" {
		events = slices.Clone(RecordEventNames)
	} else if !e.validEvents(events) {
		return "", "", refusal("invalid_events")
	}
	if !e.resourceExists(ctx, c.SiteID, resource) {
		return "", "", refusal("storage_not_found")
	}
	ctx, cancel := context.WithTimeout(ctx, e.limits.NotifyTimeout)
	defer cancel()
	if err := e.checkURL(ctx, in.Endpoint); err != nil {
		return "", "", err
	}
	if !validPushKeys(in.P256dh, in.Auth) {
		return "", "", refusal("invalid_keys")
	}
	now := time.Now()
	n := model.Notification{SiteID: c.SiteID, OwnerID: subscriber, Source: "owner", URL: in.Endpoint, Events: events, Storage: resource, Label: in.Label, Kind: "web-push", SubscriberRole: role, PushP256dh: in.P256dh, PushAuth: in.Auth, DeviceLabel: in.Label, VerifiedAt: &now, PushFamily: family}
	id, _, err = e.svc.UpsertPushSubscription(ctx, n, RandomID("push_", 12), e.limits.NotifyMaxPerSite)
	if errors.Is(err, model.ErrNotificationCap) {
		return "", "", refusal("too_many_notifications")
	}
	if err != nil {
		return "", "", err
	}
	publicKey, _, _ = e.VAPIDKeys(ctx, env)
	return id, publicKey, nil
}

// UnsubscribePush ends push for one device: a visitor may only name their
// own, the owner any on their site. Revocation stays available when web
// push is no longer offered.
func (e *Engine) UnsubscribePush(ctx context.Context, c PushCaller, id string) (bool, error) {
	role, subscriber, _, err := e.pushIdentity(ctx, c)
	if err != nil {
		return false, err
	}
	if role != "visitor" {
		subscriber = ""
	}
	return e.svc.DeleteNotificationForOwner(ctx, c.SiteID, id, subscriber)
}

// MutePush replaces one device's per-resource mute list (at most 50),
// with the same ownership rule as UnsubscribePush.
func (e *Engine) MutePush(ctx context.Context, c PushCaller, id string, resources []string) error {
	role, subscriber, _, err := e.pushIdentity(ctx, c)
	if err != nil {
		return err
	}
	n, err := e.svc.GetNotification(ctx, id)
	if err != nil || n.SiteID != c.SiteID || n.Kind != "web-push" || role == "visitor" && (n.SubscriberRole != "visitor" || n.OwnerID != subscriber) {
		return refusal("not_found")
	}
	if resources == nil {
		resources = []string{}
	}
	if len(resources) > 50 {
		return refusal("too_many_resources")
	}
	return e.svc.SetMutedResources(ctx, id, resources)
}

// OwnerPushSite is the account's site name for an owner push route, when
// it may use web push.
func (e *Engine) OwnerPushSite(ctx context.Context, accountID, name string) (model.Site, error) {
	site, err := sitesrepo.GetSiteByUser(ctx, e.svc.database, accountID, strings.ToLower(strings.TrimSpace(name)))
	if err != nil {
		return model.Site{}, refusal("site_not_found")
	}
	if err := e.pushOffered(ctx, site.ID); err != nil {
		var refused *Error
		if !errors.As(err, &refused) {
			err = &Error{Code: "internal_error", Message: "internal server error"}
		}
		return model.Site{}, err
	}
	return site, nil
}

// PushDevices lists every device subscribed on the owner's site, in the
// field names of View so the dashboard reads one shape.
func (e *Engine) PushDevices(ctx context.Context, accountID, name string) ([]map[string]any, error) {
	site, err := e.OwnerPushSite(ctx, accountID, name)
	if err != nil {
		return nil, err
	}
	devices, err := e.svc.ListPushDevices(ctx, site.ID)
	out := make([]map[string]any, 0, len(devices))
	for _, d := range devices {
		out = append(out, map[string]any{"id": d.ID, "subscriber_role": d.SubscriberRole, "label": d.DeviceLabel, "device_label": d.DeviceLabel, "storage": d.Storage, "events": d.Events, "muted_resources": d.MutedResources, "state": d.State, "created_at": d.CreatedAt, "last_success_at": d.LastSuccessAt, "last_result": d.LastResult})
	}
	return out, err
}

// DeleteSitePush turns push off on the owner's site: every device, the
// owner's and every visitor's, at once. It returns how many went.
func (e *Engine) DeleteSitePush(ctx context.Context, accountID, name string) (int64, error) {
	site, err := e.OwnerPushSite(ctx, accountID, name)
	if err != nil {
		return 0, err
	}
	return e.svc.DeletePushSubscriptionsForSite(ctx, site.ID)
}

// DeletePushDevice removes one push device (never another kind of
// subscription) on the owner's site.
func (e *Engine) DeletePushDevice(ctx context.Context, accountID, name, id string) error {
	site, err := e.OwnerPushSite(ctx, accountID, name)
	if err != nil {
		return err
	}
	n, err := e.svc.GetNotification(ctx, id)
	if err != nil || n.SiteID != site.ID || n.Kind != "web-push" {
		return refusal("not_found")
	}
	_, err = e.svc.DeleteNotification(ctx, id)
	return err
}

// PushRules are the owner's per-resource push rules.
func (e *Engine) PushRules(ctx context.Context, accountID, name string) ([]model.PushRule, error) {
	site, err := e.OwnerPushSite(ctx, accountID, name)
	if err != nil {
		return nil, err
	}
	return e.svc.ListPushRules(ctx, site.ID)
}

// SavePushRule validates and stores one rule on site: the resource must
// exist, the recipients be known, the title one plain line of at most 120
// bytes, and the body column a permitted name (columnExists: present on
// the resource, or not knowable).
func (e *Engine) SavePushRule(ctx context.Context, site model.Site, rule model.PushRule, columnExists bool) error {
	rule.Resource = strings.TrimSpace(rule.Resource)
	rule.Title = strings.TrimSpace(rule.Title)
	rule.BodyColumn = strings.TrimSpace(rule.BodyColumn)
	switch {
	case !e.resourceExists(ctx, site.ID, rule.Resource):
		return refusal("storage_not_found")
	case !slices.Contains([]string{"owner", "row_owner", "both", "none"}, rule.Recipients):
		return refusal("invalid_recipients")
	case len(rule.Title) > 120 || len(rule.BodyColumn) > 64 || !pushPlainText(rule.Title):
		return refusal("invalid_request")
	case rule.BodyColumn != "" && (!pushBodyColumnAllowed(rule.BodyColumn) || !columnExists):
		return refusal("invalid_body_column")
	}
	rule.SiteID = site.ID
	return e.svc.SetPushRule(ctx, rule)
}

// pushPlainText rejects control characters: a title shows verbatim on a
// lock screen, as exactly the one line the owner typed.
func pushPlainText(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// DeleteViewerPush removes the push devices of the visitor signed in as
// email on siteID, when they stop being a named viewer (viewers key by
// email, push rows by user id). Every device is tried; the last failure
// is returned.
func (e *Engine) DeleteViewerPush(ctx context.Context, siteID, email string) error {
	devices, err := e.svc.ListPushDevices(ctx, siteID)
	if err != nil {
		return err
	}
	var lastErr error
	for _, d := range devices {
		if d.SubscriberRole != "visitor" {
			continue
		}
		if got, err := access.VisitorEmail(ctx, e.svc.database, d.OwnerID); err != nil || !strings.EqualFold(got, email) {
			continue
		}
		if _, err := e.svc.DeleteNotification(ctx, d.ID); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
