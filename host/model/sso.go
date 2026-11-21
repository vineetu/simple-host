package model

import "database/sql"

type SSOPass struct{ Hash, UserID, ParentKeyHash string }

type SSOSignout struct{ PassHash, ReturnTo string }

type SSOHop struct {
	State, SiteID, Host, ReturnTo, NonceHash string
	ConsentHash, ConsentPassHash             sql.NullString
}

type InternalVisitorPass struct {
	UserID, SiteID, Host, AppName string
}
