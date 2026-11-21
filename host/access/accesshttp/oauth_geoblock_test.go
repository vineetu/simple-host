package accesshttp

import (
	"testing"
)

// buildGeo supplies a local country lookup mapping ip to iso.
func buildGeo(t *testing.T, ip, iso string) countryLookup {
	t.Helper()
	return countryLookup{ip: iso}
}
