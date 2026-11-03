package hackevents

import (
	"strings"
	"unicode"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// AgeRangeOrder and GenderOrder are the fixed product choices GET
// .../apply's "fields" lists, in display order; "" (not shared) is always
// accepted too and is not listed. Was hack_apply.go's hackAgeRangeOrder and
// hackGenderOrder.
var (
	AgeRangeOrder  = []string{"under_18", "18_24", "25_34", "35_plus"}
	AgeRangeLabels = map[string]string{
		"under_18": "Under 18",
		"18_24":    "18-24",
		"25_34":    "25-34",
		"35_plus":  "35+",
	}
	GenderOrder  = []string{"woman", "man", "non_binary", "another", "prefer_not_to_say"}
	GenderLabels = map[string]string{
		"woman":             "Woman or girl",
		"man":               "Man or boy",
		"non_binary":        "Non-binary",
		"another":           "Another gender",
		"prefer_not_to_say": "Prefer not to say",
	}
)

// CountryName is the participants CSV's country cell: the English region
// name for a stored ISO 3166-1 alpha-2 code, blank when blank. Was
// hack_apply.go's hackCountryName.
func CountryName(code string) string {
	if code == "" {
		return ""
	}
	region, err := language.ParseRegion(code)
	if err != nil {
		return ""
	}
	return display.English.Regions().Name(region)
}

// JoinedViaLabel is the participants CSV's joined_via cell. Was
// hack_apply.go's hackJoinedViaLabel.
func JoinedViaLabel(joinedVia string) string {
	if joinedVia == "application" {
		return "application"
	}
	return "join link"
}

// StripInvisible drops every character that is not seen (format
// characters, variation selectors, fillers), so two team names that look the
// same are the same name. An emoji sequence may lose its joiners: a team
// name is a label, not a place for composed emoji. Was hack_teams.go's
// stripInvisible.
func StripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Cf, r),
			r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF,
			r >= 0x180B && r <= 0x180F, r == 0x034F, r == 0x17B4, r == 0x17B5,
			r == 0x2065, r >= 0xFFF0 && r <= 0xFFF8, r >= 0x1D173 && r <= 0x1D17A, r >= 0xE0000 && r <= 0xE0FFF,
			r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0:
			return -1
		}
		return r
	}, s)
}
