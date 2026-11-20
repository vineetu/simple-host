package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"github.com/vsriram/simple-host/host/model"
)

// ------------------------------------------------------------- the payload

// pushPushSkip is the sentinel Body (deps.Body) returns for a delivery that
// must not actually be sent: muted by this device, or ruled out by the
// owner's recipients control for this resource. sendPush recognises it and
// reports a clean, non-retried, non-failing outcome.
var pushPushSkip = []byte("null")

// pushPayload builds the small, generic-by-default payload a push service
// and a lock screen show -- never the row's own content unless the owner
// named one column for this resource, and the dashboard says so. It also
// carries the mute and recipients gates, since both need the event's own
// resource (data["storage"]) together with n, which only this function
// (not sendPush, which only sees the rendered body) has at hand.
// pushPayloadDefer is the sentinel Body (deps.Body) returns on a genuine
// failure (decode error, a push-rule lookup that could not be answered):
// the delivery must be retried, never silently delivered with recipients
// wider than configured. nil, not pushPushSkip, so ProcessDelivery leaves
// the claim to expire and be retried rather than finishing it as handled.
var pushPayloadDefer []byte

func (h *DataHandler) pushPayload(ctx context.Context, n model.Notification, d model.NotificationDelivery) []byte {
	var data map[string]any
	dec := json.NewDecoder(bytes.NewReader(d.Data))
	dec.UseNumber()
	if dec.Decode(&data) != nil {
		return pushPushSkip
	}
	resource, _ := data["storage"].(string)
	siteName, _ := data["site"].(string)
	siteURL, _ := data["site_url"].(string)
	// A mute applies to the event's own resource regardless of what this
	// device's own subscription filter is scoped to: a site-wide device
	// can mute one table without unsubscribing from the rest.
	for _, muted := range n.MutedResources {
		if muted == resource {
			return pushPushSkip
		}
	}
	rule, err := h.notificationsSvc().GetPushRule(ctx, n.SiteID, resource)
	if err != nil {
		// A real lookup failure must defer, never silently fall back to
		// "both" -- that would widen recipients past whatever the owner
		// configured (possibly "none") for as long as the failure lasts.
		return pushPayloadDefer
	}
	switch rule.Recipients {
	case "none":
		return pushPushSkip
	case "owner":
		if n.SubscriberRole == "visitor" {
			return pushPushSkip
		}
	case "row_owner":
		if n.SubscriberRole != "visitor" {
			return pushPushSkip
		}
	}
	title := strings.TrimSpace(rule.Title)
	if title == "" {
		title = "New activity"
		if siteName != "" {
			title = siteName + " has an update"
		}
	}
	body := "Something was added or changed."
	if rule.BodyColumn != "" && pushBodyColumnAllowed(rule.BodyColumn) {
		if fields, ok := data["fields"].(map[string]any); ok {
			// Even a permitted column's actual value falls back to
			// generic text if it looks like an email or IP address
			// (adversary review, item A, binding): the owner chose the
			// column, but a user-typed "notes" or "message" field can
			// still happen to contain one, and the blacklist above only
			// ever looks at the column's name, never its content.
			if s, ok := pushScalarString(fields[rule.BodyColumn]); ok && s != "" && !pushValueLooksSensitive(s) {
				body = notifyCut(s, 200)
			}
		}
	}
	payload := map[string]any{"title": notifyCut(title, 120), "body": body, "url": siteURL, "tag": d.Name}
	raw, err := json.Marshal(payload)
	if err != nil {
		return pushPushSkip
	}
	return raw
}

// pushForbiddenBodyColumns are exact (case-insensitive) column names a
// push rule's body_column may never name: identity and network fields
// that must never reach a lock screen, even though the owner's own
// dashboard only offers columns that exist on the resource (defense in
// depth: this is re-checked here, at render time, not only at save time).
// Kept for names pushSplitColumnWords would not otherwise catch (no
// separator at all, e.g. "ipaddress"); pushForbiddenBodyColumnWords
// below is what actually catches most real-world variants.
var pushForbiddenBodyColumns = map[string]bool{
	"email": true, "emails": true, "visitor_email": true, "visitor_emails": true,
	"contact_email": true, "owner_email": true,
	"ip": true, "ip_address": true, "ipaddress": true, "remote_ip": true, "client_ip": true,
	"visitor_id": true, "visitorid": true, "owner_id": true, "user_id": true, "userid": true,
	"password": true, "passcode": true, "secret": true, "token": true, "api_key": true, "apikey": true,
	"auth": true, "session": true, "session_id": true, "cookie": true,
	"ssn": true, "phone": true, "phone_number": true, "address": true,
}

// pushForbiddenBodyColumnWords blocks a column name that contains any of
// these as a whole underscore/hyphen/camelCase-separated word, not only
// an exact match (adversary review, item A: "email_address",
// "customerEmail" and "ip_addr" all slipped past the exact-name
// blacklist above, since none of them equal "email" or "ip" exactly).
// Harmless names like "description" or "note" are unaffected: they
// contain none of these words.
var pushForbiddenBodyColumnWords = map[string]bool{
	"email": true, "mail": true, "ip": true, "phone": true, "tel": true,
	"telephone": true, "address": true, "ssn": true,
	"password": true, "passcode": true, "secret": true, "token": true,
	"apikey": true, "api": true, "auth": true, "session": true, "cookie": true,
}

// pushForbiddenBodyColumnIDPhrases: paired with a following "id" word
// (visitor_id, ownerID, user-id, ...), these name a specific person, not
// a harmless reference like "order_id" or "team_id". "id" alone is too
// common a column name to block outright.
var pushForbiddenBodyColumnIDPhrases = map[string]bool{
	"visitor": true, "owner": true, "user": true, "client": true,
	"account": true, "customer": true, "person": true,
}

// pushSplitColumnWords splits a column name into lowercase words on
// underscores, hyphens, spaces and camelCase boundaries: "email_address",
// "emailAddress" and "EmailAddress" all split to ["email", "address"].
func pushSplitColumnWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = nil
		}
	}
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
		case unicode.IsUpper(r) && i > 0 && !unicode.IsUpper(runes[i-1]):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return words
}

func pushBodyColumnAllowed(name string) bool {
	trimmed := strings.TrimSpace(name)
	if pushForbiddenBodyColumns[strings.ToLower(trimmed)] {
		return false
	}
	// pushSplitColumnWords needs the original case to find camelCase
	// boundaries ("customerEmail"); lowercasing first (as the exact-name
	// check above does) would destroy exactly the signal it looks for.
	words := pushSplitColumnWords(trimmed)
	for i, w := range words {
		if pushForbiddenBodyColumnWords[w] {
			return false
		}
		if w == "id" && i > 0 && pushForbiddenBodyColumnIDPhrases[words[i-1]] {
			return false
		}
	}
	return true
}

// pushEmailLikePattern/pushIPv4LikePattern/pushIPv6LikePattern are cheap,
// deliberately loose checks (adversary review, item A, binding rule): a
// permitted column's actual value still falls back to generic text if it
// looks like an email address or an IP, regardless of what the column is
// named -- a free-text "notes"/"message" field can contain one even when
// its own name is perfectly innocuous.
var (
	pushEmailLikePattern = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)
	pushIPv4LikePattern  = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	pushIPv6LikePattern  = regexp.MustCompile(`[0-9a-fA-F]{1,4}:[0-9a-fA-F:]{2,}[0-9a-fA-F]`)
)

func pushValueLooksSensitive(s string) bool {
	return pushEmailLikePattern.MatchString(s) || pushIPv4LikePattern.MatchString(s) || pushIPv6LikePattern.MatchString(s)
}

// pushScalarString returns v's displayable text, ok only for a JSON
// scalar (string, number, bool); an object or array is refused rather
// than stringified, since a structured value could carry fields the
// owner never intended to show (SIM-360 privacy guardrail: the push
// payload never carries more than the one named column's plain value).
func pushScalarString(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		return t, true
	case json.Number:
		return t.String(), true
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	default:
		return "", false
	}
}
