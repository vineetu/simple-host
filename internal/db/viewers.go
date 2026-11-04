package db

import (
	"github.com/vsriram/simple-host/internal/model"
)

// Named viewers (handler/viewers.go): sites.access is 'anyone' or 'specific';
// at 'specific' only the owner and the emails in site_viewers can open the
// site.

// SiteViewer is one named viewer of a site. SignedInAt is nil until that
// email has signed in to this site at least once (site_visitor_signins,
// matched through the account the email signs in as: an email-code account's
// username is that exact email, an OAuth account's is its oauth_identities
// row).
type SiteViewer = model.SiteViewer
