package accesstest

// countryLookup is a local country lookup fake shared by sign-in tests.
type countryLookup map[string]string

func (g countryLookup) CountryISO(ip string) string { return g[ip] }
