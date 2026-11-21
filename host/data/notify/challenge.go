package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// challengeClient is the webhook client for one verification or test send
// from owner to raw: it spends the owner's, the host's and each resolved
// address's challenge budget (Failure "rate_limited" when one is spent),
// then dials exactly the addresses it charged.
func (e *Engine) challengeClient(ctx context.Context, owner, raw string) (client, error) {
	c := e.client
	u, err := url.Parse(raw)
	if err != nil {
		return c, Failure("invalid_url")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if !e.challenge.Allow("owner:"+owner) || !e.challenge.Allow("host:"+host) {
		return c, Failure("rate_limited")
	}
	addrs, err := c.addresses(ctx, host)
	if err != nil {
		return c, err
	}
	for _, addr := range addrs {
		if !e.challenge.Allow("ip:" + addr.IP.String()) {
			return c, Failure("rate_limited")
		}
	}
	c.resolver = func(context.Context, string) ([]net.IPAddr, error) { return addrs, nil }
	return c, nil
}

// verifySlot takes one of the concurrent verification slots; release
// returns it. ok is false when all are taken.
func (e *Engine) verifySlot() (release func(), ok bool) {
	select {
	case e.verifying <- struct{}{}:
		return func() { <-e.verifying }, true
	default:
		return nil, false
	}
}

// Test sends one test event to the subject's subscription through its real
// delivery path: a signed webhook, or for a push device a fixed generic
// payload (a test confirms the device works whatever the push rules say).
// siteName and siteURL are the site as payloads show it. A refused
// receiver or an offline site is a Result, not an error.
func (e *Engine) Test(ctx context.Context, env Env, s Subject, siteName, siteURL string) (Result, error) {
	n := s.Notification
	if n.Source == "mcp" || n.ExpiresAt != nil && !n.ExpiresAt.After(time.Now()) {
		return Result{}, refusal("client_subscription")
	}
	release, ok := e.verifySlot()
	if !ok {
		return Result{}, refusal("busy")
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, e.limits.NotifyTimeout)
	defer cancel()
	c, err := e.challengeClient(ctx, s.Person, n.URL)
	switch {
	case errors.Is(err, Failure("rate_limited")):
		return Result{}, refusal("rate_limited")
	case err != nil:
		return Result{Result: Category(err)}, nil
	case !s.Site.Live():
		return Result{Result: "site_unavailable"}, nil
	}
	event := model.NotificationDelivery{EventID: RandomID("evt_test_", 16), Name: "notification.test", CreatedAt: time.Now()}
	event.Data, _ = json.Marshal(map[string]any{"v": 1, "site": siteName, "site_url": siteURL, "test": true})
	if n.Kind == "web-push" {
		payload, _ := json.Marshal(map[string]any{"title": "Test notification", "body": "Your push notification is working.", "url": siteURL, "tag": "notification.test"})
		return e.sendPush(ctx, env, n, payload), nil
	}
	cursor, err := e.svc.NotificationCursor(ctx, n.ID, n.SiteID)
	if err != nil {
		return Result{}, err
	}
	return c.send(ctx, env, n, event.EventID, Body(event, cursor, e.limits.NotifyBodyMaxBytes)), nil
}
