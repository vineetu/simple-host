package httpx

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode"
)

// AcceptsMarkdown reports whether r's Accept header prefers text/markdown.
// Only explicit markdown and HTML preferences matter here. Equal preferences
// favor markdown; a browser's usual HTML/wildcard Accept still gets HTML.
func AcceptsMarkdown(r *http.Request) bool {
	var markdown, htmlQuality float64
	for _, value := range strings.Split(strings.Join(r.Header.Values("Accept"), ","), ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		quality := 1.0
		if q, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(q, 64)
			if err != nil || quality < 0 || quality > 1 {
				continue
			}
		}
		switch media {
		case "text/markdown":
			markdown = max(markdown, quality)
		case "text/html":
			htmlQuality = max(htmlQuality, quality)
		}
	}
	return markdown > 0 && markdown >= htmlQuality
}

// CSVSafe stops a visitor-supplied value being read as a formula when the
// owner opens the export in Excel, Sheets or Numbers. A text cell that starts
// with = + - @, a tab or a carriage return gets a leading apostrophe, which
// spreadsheets show as plain text. JSON numbers never reach here, so a
// negative number stays a number.
//
// Security review (Grok 7): a leading Unicode format character (category
// Cf, e.g. U+200E LEFT-TO-RIGHT MARK) or run of whitespace does not change
// what a spreadsheet does with the formula that follows it, but it does
// move the formula past byte 0, where the check above never saw it. Walk
// past those before testing the first real character, so "<LRM>=HYPERLINK(...)"
// is still caught; the apostrophe is still prefixed to the original
// string, not the trimmed one, so nothing is lost from the cell.
func CSVSafe(s string) string {
	if s == "" {
		return s
	}
	trimmed := strings.TrimLeftFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.Is(unicode.Cf, r)
	})
	if trimmed == "" {
		return s
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}
