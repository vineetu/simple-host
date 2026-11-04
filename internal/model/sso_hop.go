package model

import "database/sql"

type SSOHop struct {
	State, SiteID, Host, ReturnTo, NonceHash string
	ConsentHash, ConsentPassHash             sql.NullString
}
