package model

import (
	"encoding/json"
	"errors"
	"time"
)

type Notification struct {
	Event           string     `json:"event,omitempty"` // organiser website slug, empty for team/Host sites
	Hack            bool       `json:"-"`               // checked under the membership lock when saving
	ID              string     `json:"id"`
	SiteID          string     `json:"site_id"`
	OwnerID         string     `json:"owner_id"`
	Site            string     `json:"site"`
	Source          string     `json:"source"`
	GrantID         *string    `json:"grant_id"`
	AppName         string     `json:"app_name"`
	Label           string     `json:"label"`
	URL             string     `json:"url"`
	Events          []string   `json:"events"`
	Storage         string     `json:"storage"`
	ArgsCanonical   string     `json:"args_canonical"`
	Secret          string     `json:"secret"`
	PrevSecret      string     `json:"prev_secret"`
	PrevSecretUntil *time.Time `json:"prev_secret_until"`
	AuthHeaderName  string     `json:"auth_header_name"`
	AuthHeaderValue string     `json:"auth_header_value"`
	// Kind is "webhook" (default), "mcp-event" or "web-push" (SIM-360).
	// SubscriberRole is "owner" (the site owner's device/endpoint) or
	// "visitor" (a signed-in visitor's own device; OwnerID is then that
	// visitor's own users.id, never another person's). PushP256dh/PushAuth
	// are the Web Push subscription's keys; URL holds the push service
	// endpoint for a web-push row. DeviceLabel is a human label for the
	// dashboard's device list. MutedResources lets one device mute
	// specific tables without unsubscribing entirely.
	Kind           string   `json:"kind"`
	SubscriberRole string   `json:"subscriber_role"`
	PushP256dh     string   `json:"-"`
	PushAuth       string   `json:"-"`
	DeviceLabel    string   `json:"device_label,omitempty"`
	MutedResources []string `json:"muted_resources,omitempty"`
	// PushFamily is the creating browser session's own scoping key (FIX
	// 10): the session's SSO pass hash when it is part of a signed-in-
	// everywhere family, else the session's own id -- never delivered
	// or exposed (json:"-"), used only to scope a sign-out's push
	// cleanup to the one session (or family of sessions) that is
	// actually signing out, not every device this same person has ever
	// subscribed, on this site or any other.
	PushFamily    string     `json:"-"`
	State         string     `json:"state"`
	VerifiedAt    *time.Time `json:"verified_at"`
	ExpiresAt     *time.Time `json:"expires_at"`
	FailingSince  *time.Time `json:"failing_since"`
	LastAttemptAt *time.Time `json:"last_attempt_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	LastResult    string     `json:"last_result"`
	CreatedAt     time.Time  `json:"created_at"`
}

// NotificationSecrets uses the handler's passcode AEAD without giving the DB a key.
type NotificationSecrets struct {
	Seal func(id, plaintext string) (string, error)
	Open func(id, stored string) (string, error)
}
type NotificationDelivery struct {
	Seq            int64
	SubscriptionID string
	Attempts       int
	LeaseUntil     time.Time
	LastAttemptAt  time.Time
	EventID        string
	Name           string
	Data           json.RawMessage
	CreatedAt      time.Time
}

// PushRule is the owner's per-site, per-resource (resource "" is the site
// default) web-push routing and payload control (SIM-360).
type PushRule struct {
	SiteID     string `json:"-"`
	Resource   string `json:"resource"`
	Recipients string `json:"recipients"` // owner | row_owner | both | none
	Title      string `json:"title"`
	BodyColumn string `json:"body_column"`
}

var ErrNotificationCap = errors.New("too many notifications")
var ErrNotificationAccess = errors.New("notification access ended")
var ErrNotificationSite = errors.New("subscription site changed")
