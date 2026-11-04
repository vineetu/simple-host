package model

import (
	"database/sql"
	"time"
)

// EstablishToken is the one-time bounce from the apex callback onto the host
// that will Set-Cookie.
type EstablishToken struct {
	Once      string
	SessionID []byte
	Host      string
	ReturnTo  string
	NonceHash sql.NullString
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    sql.NullTime
}
