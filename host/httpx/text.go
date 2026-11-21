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
// Leading whitespace and Unicode format characters (U+200E and the like) do
// not stop a spreadsheet running the formula after them, so the test skips
// them; the apostrophe still goes on the original string.
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
