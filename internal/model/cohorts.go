package model

import (
	"database/sql"
	"time"
)

// ConnectedApp is one AI app an account connected through the /mcp consent
// page, and when it first did.
type ConnectedApp struct {
	Name  string    `json:"name"`
	First time.Time `json:"first_at"`
}

// Activation is what an account did after joining, for the admin page's
// sign-ups by source drill-down. Apps come from oauth_grants (disconnecting an
// app deletes its grant, so a disconnected app is gone from here); sites count
// every site that ever had a published version, including deleted ones not
// yet purged; Visits are page views by people (not bots) to all of them.
type Activation struct {
	Apps           []ConnectedApp
	SitesPublished int
	FirstPublish   sql.NullTime
	Visits         int64
	LastActive     sql.NullTime
}
