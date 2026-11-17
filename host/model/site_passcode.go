package model

import "database/sql"

// SitePasscodeRow is a site's passcode state (handler/passcode.go). Enc is the
// sealed passcode (nil = none); Generation goes up on every change and on
// "sign everyone out".
type SitePasscodeRow struct {
	SiteID     string
	UserID     string
	Name       string
	Enc        []byte
	SetAt      sql.NullTime
	Generation int
	Offline    bool
	// NamedViewers: sites.access is 'specific' (viewers.go): only the owner
	// and the named viewers may open the site.
	NamedViewers bool
}
