package hackevents

import (
	"strings"
	"testing"
)

// TestLineTable is the single-line field rule (event title, team name,
// organiser name) that the HTTP create/patch tests used to prove one
// request at a time: real text in any script is kept, line breaks, bidi
// overrides, control bytes and blank-looking names are refused.
func TestLineTable(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"Spring hack", "Spring hack", true},
		{"Night  Owls", "Night Owls", true},
		{"Family 👨‍👩‍👧", "Family 👨‍👩‍👧", true},
		{"Glasgow Hack 🏴\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", "", true},
		{"مرحبا ‏ABC", "", true},
		{"A\nB", "", false},
		{"Bidi ‮gnp.exe", "", false},
		{"hi\x00there", "", false},
		{"a b", "", false},
		{"ㅤ", "", false},
		{"⠀⠀", "", false},
		{"", "", false},
		{strings.Repeat("t", 121), "", false},
	} {
		got, err := Line(tc.in, "title", 1, 120)
		if (err == nil) != tc.ok {
			t.Errorf("Line(%q): err %v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if err != nil && err.Code != "invalid_title" {
			t.Errorf("Line(%q): code %q", tc.in, err.Code)
		}
		if tc.ok && tc.want != "" && got != tc.want {
			t.Errorf("Line(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStripInvisibleTable: every invisible mark goes before team names are
// compared, so a lookalike cannot take a name already in use.
func TestStripInvisibleTable(t *testing.T) {
	for _, in := range []string{"Race⁠ Two", "Ra­ce Two", "Ra‍ce Two", "Ra⁡ce Two", "Ra️ce Two", "Ra᠋ce Two", "Ra͏ce Two"} {
		if got := StripInvisible(in); got != "Race Two" {
			t.Errorf("StripInvisible(%q) = %q", in, got)
		}
	}
}
