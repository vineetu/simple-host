package model

import (
	"time"
)

// SiteViewer is one named viewer of a site. SignedInAt is nil until that
// email has signed in to this site at least once (site_visitor_signins,
// matched through the account the email signs in as: an email-code account's
// username is that exact email, an OAuth account's is its oauth_identities
// row).
type SiteViewer struct {
	Email      string     `json:"email"`
	AddedAt    time.Time  `json:"added_at"`
	SignedInAt *time.Time `json:"signed_in_at,omitempty"`
}
