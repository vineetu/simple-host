package model

import (
	"database/sql"
	"time"
)

type OAuthClient struct {
	ClientID                string
	SecretHash              sql.NullString
	Name                    string
	RedirectURIs            []string
	TokenEndpointAuthMethod string
	PKCERequired            bool
	Dynamic                 bool
	CreatedAt               time.Time
}
