package accounts

import "strings"

// CountryLookup resolves an address to an ISO-3166 alpha-2 country code.
// An empty result means the country is unknown.
type CountryLookup interface {
	CountryISO(ip string) string
}

// SignupGeoBlock decides whether sign-in or sign-up may proceed from the
// country an IP resolves to. SIGNUP_BLOCKED_COUNTRIES is empty by default,
// which turns this off entirely: the zero value never blocks anything.
//
// A listed country refuses every sign-in and sign-up attempt from it — a
// brand-new account and an existing one alike (owner decision 2026-10-01:
// the data shows no API activity from these countries, so simplicity won
// over carving out a hypothetical existing account). API-key requests and
// site viewing are untouched: an existing account's key keeps publishing
// regardless of where a request comes from, since that is not a sign-in. A
// country the local geo database (internal/geoip) cannot resolve is always
// allowed.
type SignupGeoBlock struct {
	geo     CountryLookup
	blocked map[string]bool
}

// NewSignupGeoBlock builds the guard from the resolved geo database and the
// configured country list (SignupBlockedCountries). An empty list, or a nil
// geo database, yields a zero value that always allows.
func NewSignupGeoBlock(geo CountryLookup, countries []string) SignupGeoBlock {
	if len(countries) == 0 {
		return SignupGeoBlock{}
	}
	blocked := make(map[string]bool, len(countries))
	for _, c := range countries {
		if c = strings.ToUpper(strings.TrimSpace(c)); c != "" {
			blocked[c] = true
		}
	}
	if len(blocked) == 0 {
		return SignupGeoBlock{}
	}
	return SignupGeoBlock{geo: geo, blocked: blocked}
}

// BlockedCountry reports the ISO-3166 alpha-2 country ip resolves to (local
// lookup only — never sent anywhere), and whether that country is on the
// sign-in/sign-up blocklist. It always returns false when the feature is
// off, the geo database is unset, ip is empty, or the country cannot be
// resolved.
func (g SignupGeoBlock) BlockedCountry(ip string) (country string, blocked bool) {
	if len(g.blocked) == 0 || g.geo == nil || ip == "" {
		return "", false
	}
	cc := strings.ToUpper(g.geo.CountryISO(ip))
	if cc == "" {
		return "", false
	}
	return cc, g.blocked[cc]
}
