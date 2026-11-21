package web

import (
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
)

// A visitor-supplied value must never reach the owner's spreadsheet as a
// formula: text starting with = + - @ tab or CR is neutralised, numbers are not.
func TestCSVSafeNeutralisesFormulas(t *testing.T) {
	cases := map[string]string{
		"=cmd|'/C calc'!A1":        "'=cmd|'/C calc'!A1",
		"+HYPERLINK(\"http://x\")": "'+HYPERLINK(\"http://x\")",
		"@SUM(A1)":                 "'@SUM(A1)",
		"hello":                    "hello",
		"":                         "",
	}
	for in, want := range cases {
		if got := httpx.CSVSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
	// Security review (Grok 7): a leading LEFT-TO-RIGHT MARK (U+200E, a
	// Unicode format character) must not hide the formula that follows it.
	if got := httpx.CSVSafe("‎=HYPERLINK(\"https://evil.example\",\"click\")"); got != "'‎=HYPERLINK(\"https://evil.example\",\"click\")" {
		t.Errorf("csvSafe did not escape past a leading format character: %q", got)
	}
	if got := httpx.CSVSafe("  =HYPERLINK(1)"); got != "'  =HYPERLINK(1)" {
		t.Errorf("csvSafe did not escape past leading whitespace: %q", got)
	}
}
