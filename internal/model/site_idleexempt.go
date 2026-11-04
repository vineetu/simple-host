package model

type IdleExempt struct {
	Handles       []string // lowercased handles
	ReviewerEmail string   // REVIEW_ACCOUNT_EMAIL, "" when none
	// FamilySites (IDLE_EXEMPT_FAMILY_SITES): a site whose main address is a
	// verified address family of its account is kept, like one with a
	// domain of its own.
	FamilySites bool
}
