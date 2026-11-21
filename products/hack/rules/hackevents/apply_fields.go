package hackevents

import (
	"golang.org/x/text/language"
	"regexp"
	"strings"
)

// ApplicationsClosedReason reports why applications are closed, or nil when open.
func ApplicationsClosedReason(open, listed bool, stage string) any {
	if !open {
		return "applications_off"
	}
	if !listed {
		return "not_listed"
	}
	if stage == "open" || stage == "building" {
		return nil
	}
	return "signup_over"
}

// countryCodeShape is two ASCII letters.
var countryCodeShape = regexp.MustCompile(`^[A-Z]{2}$`)

// countryExcluded is the gap between language.Region.IsCountry() plus "is its own canonical form" and an actual, choosable country, matched to the fixed list screens/public-apply.js offers.
var countryExcluded = map[string]bool{
	"AN": true, "CS": true, "EZ": true, "FQ": true, "NT": true, "PC": true, "SU": true, "UN": true, "YU": true,
	"AC": true, "CP": true, "DG": true, "EA": true, "IC": true, "TA": true,
	"CQ": true,
}

// Country normalizes and validates an optional ISO country code.
func Country(s string) (string, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return "", true
	}
	if !countryCodeShape.MatchString(s) {
		return "", false
	}
	region, err := language.ParseRegion(s)
	if err != nil || !region.IsCountry() || region.Canonicalize() != region || countryExcluded[s] {
		return "", false
	}
	return region.String(), true
}

// Enum reports whether an optional value is in the allowed list.
func Enum(s string, allowed []string) bool {
	if s == "" {
		return true
	}
	for _, v := range allowed {
		if s == v {
			return true
		}
	}
	return false
}
