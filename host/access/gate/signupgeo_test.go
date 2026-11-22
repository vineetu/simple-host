package gate

import (
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
)

// countryLookup is a local country lookup fake shared by sign-in tests.
type countryLookup map[string]string

func (g countryLookup) CountryISO(ip string) string { return g[ip] }

// geoBlockFor builds a accounts.SignupGeoBlock whose lookup maps ip to iso.
func geoBlockFor(t *testing.T, ip, iso string, blocked []string) accounts.SignupGeoBlock {
	t.Helper()
	return accounts.NewSignupGeoBlock(countryLookup{ip: iso}, blocked)
}
