package edition

// IdentityPolicy is the account and credential policy (handles, personal sites, holding accounts, team keys).
type IdentityPolicy struct{}

func identityFor(k Kind) IdentityPolicy {
	return IdentityPolicy{}
}
