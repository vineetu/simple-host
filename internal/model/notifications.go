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
	State           string     `json:"state"`
	VerifiedAt      *time.Time `json:"verified_at"`
	ExpiresAt       *time.Time `json:"expires_at"`
	FailingSince    *time.Time `json:"failing_since"`
	LastAttemptAt   *time.Time `json:"last_attempt_at"`
	LastSuccessAt   *time.Time `json:"last_success_at"`
	LastResult      string     `json:"last_result"`
	CreatedAt       time.Time  `json:"created_at"`
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

var ErrNotificationCap = errors.New("too many notifications")
var ErrNotificationAccess = errors.New("notification access ended")
var ErrNotificationSite = errors.New("subscription site changed")
