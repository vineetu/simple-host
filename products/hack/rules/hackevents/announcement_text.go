package hackevents

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Announcement text is plain text with a tiny, safe subset of Markdown:
//
//	[label](https://example.com)   a labelled link
//	https://example.com            a bare address, linked automatically
//	**bold**                       bold
//	- item                         a bullet line
//	blank line                     a new paragraph; a single line break stays a line break
//
// Nothing else is interpreted, and no raw HTML is ever produced from the
// text: every piece of it is escaped, and only http and https addresses
// become links. An address alone on its line becomes a card when it is a
// call (Zoom, Google Meet, Microsoft Teams) or a YouTube video; the card is
// worked out from the address alone, with no request to the linked page.
//
// One parser serves the web output (AnnouncementHTML), the email's HTML part
// (AnnouncementEmailHTML) and the email's text part (AnnouncementText).

const announcementMaxURL = 2000

type annInline struct {
	kind     byte // 't' text, 'b' bold, 'l' link
	text     string
	url      string // link: the cleaned address
	children []annInline
}

type annCard struct {
	service, action, label, url, host string
	videoID                           string // YouTube only
}

type annBlock struct {
	kind  string // "p", "ul", "card"
	lines [][]annInline
	card  annCard
}

var annBareURL = regexp.MustCompile(`(?i)^https?://[^\s\p{Z}<>"]+`)
var annHostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)
var annVideoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// cleanAnnouncementURL returns the address to put in an href, or false when
// the text is not a safe http(s) address. It rejects other schemes (javascript:,
// data:, ...), user info (https://zoom.us@evil.example), control, space and
// invisible formatting characters, and non-ASCII host names. The result is pure
// ASCII (non-ASCII path bytes are percent-encoded).
func cleanAnnouncementURL(raw string) (string, bool) {
	if raw == "" || len(raw) > announcementMaxURL {
		return "", false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" || u.User != nil {
		return "", false
	}
	host := u.Hostname()
	if host == "" || len(host) > 253 || !annHostName.MatchString(host) {
		return "", false
	}
	if p := u.Port(); p != "" && len(p) > 5 {
		return "", false
	}
	out := u.String()
	var b strings.Builder
	for i := 0; i < len(out); i++ {
		c := out[i]
		if c >= 0x80 || c == '"' || c == '<' || c == '>' || c == '\'' || c == '`' || c == '\\' {
			b.WriteString("%" + strings.ToUpper(hex2(c)))
			continue
		}
		b.WriteByte(c)
	}
	return b.String(), true
}

func hex2(c byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[c>>4], digits[c&15]})
}

// parseAnnouncementInline splits one line into text, bold and links.
func parseAnnouncementInline(s string, allowBold bool) []annInline {
	var out []annInline
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, annInline{kind: 't', text: text.String()})
			text.Reset()
		}
	}
	for i := 0; i < len(s); {
		rest := s[i:]
		switch {
		case allowBold && strings.HasPrefix(rest, "**"):
			if j := strings.Index(rest[2:], "**"); j > 0 {
				inner := rest[2 : 2+j]
				if strings.TrimSpace(inner) != "" {
					flush()
					out = append(out, annInline{kind: 'b', children: parseAnnouncementInline(inner, false)})
					i += 2 + j + 2
					continue
				}
			}
		case rest[0] == '[':
			if label, addr, n, ok := scanLabelledLink(rest); ok {
				flush()
				out = append(out, annInline{kind: 'l', text: label, url: addr})
				i += n
				continue
			}
		case (rest[0] == 'h' || rest[0] == 'H') && !wordBefore(s[:i]):
			if m := annBareURL.FindString(rest); m != "" {
				trimmed := trimURLTail(m)
				if addr, ok := cleanAnnouncementURL(trimmed); ok {
					flush()
					out = append(out, annInline{kind: 'l', text: trimmed, url: addr})
					i += len(trimmed)
					continue
				}
			}
		}
		// Copy one rune.
		_, size := utf8.DecodeRuneInString(rest)
		text.WriteString(rest[:size])
		i += size
	}
	flush()
	return out
}

// wordBefore reports whether the text before an address ends in a letter,
// digit or underscore, in which case the address is part of a word and is
// not linked (xhttps://...). Punctuation and spaces in any script are not.
func wordBefore(before string) bool {
	r, _ := utf8.DecodeLastRuneInString(before)
	return r != utf8.RuneError && (r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r))
}

// scanLabelledLink reads "[label](address)" at the start of s.
func scanLabelledLink(s string) (label, addr string, n int, ok bool) {
	end := strings.IndexByte(s, ']')
	if end < 2 || end+1 >= len(s) || s[end+1] != '(' {
		return
	}
	label = s[1:end]
	if strings.ContainsAny(label, "[]") || strings.TrimSpace(label) == "" {
		return
	}
	// The address ends at the ")" that balances the opening "(", so an
	// address with its own parentheses (Go_(programming_language)) stays whole.
	closeAt, depth := -1, 0
	for k := end + 2; k < len(s); k++ {
		if s[k] == '(' {
			depth++
		} else if s[k] == ')' {
			if depth == 0 {
				closeAt = k - (end + 2)
				break
			}
			depth--
		}
	}
	if closeAt < 0 {
		return
	}
	raw := s[end+2 : end+2+closeAt]
	cleaned, good := cleanAnnouncementURL(raw)
	if !good {
		return
	}
	return strings.TrimSpace(label), cleaned, end + 2 + closeAt + 1, true
}

// trimURLTail drops sentence punctuation that follows an address in prose.
func trimURLTail(m string) string {
	for len(m) > 0 {
		c := m[len(m)-1]
		switch c {
		case '.', ',', ';', ':', '!', '?', '\'', '*', ']', '}':
			m = m[:len(m)-1]
			continue
		case ')':
			if strings.Count(m, "(") < strings.Count(m, ")") {
				m = m[:len(m)-1]
				continue
			}
		}
		break
	}
	return m
}

// classifyAnnouncementCard says whether an address alone on a line is a call
// or a video, from its host and path only.
func classifyAnnouncementCard(addr, label string) (annCard, bool) {
	u, err := url.Parse(addr)
	if err != nil {
		return annCard{}, false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	c := annCard{url: addr, label: label, host: host}
	switch {
	case host == "zoom.us" || strings.HasSuffix(host, ".zoom.us"):
		c.service, c.action = "Zoom", "Join the call"
	case host == "meet.google.com":
		c.service, c.action = "Google Meet", "Join the call"
	case host == "teams.microsoft.com" || host == "teams.live.com":
		c.service, c.action = "Microsoft Teams", "Join the call"
	case host == "youtu.be":
		c.videoID = strings.Trim(u.Path, "/")
		c.service, c.action = "YouTube", "Watch the video"
	case host == "youtube.com" || host == "www.youtube.com" || host == "m.youtube.com" || host == "music.youtube.com":
		c.service, c.action = "YouTube", "Watch the video"
		if u.Path == "/watch" {
			c.videoID = u.Query().Get("v")
		} else {
			for _, p := range []string{"/embed/", "/shorts/", "/live/", "/v/"} {
				if strings.HasPrefix(u.Path, p) {
					c.videoID = strings.Trim(strings.TrimPrefix(u.Path, p), "/")
				}
			}
		}
	default:
		return annCard{}, false
	}
	if c.service == "YouTube" && !annVideoID.MatchString(c.videoID) {
		return annCard{}, false
	}
	return c, true
}

// cardLine reads a line that holds nothing but one address (bare, or as one
// labelled link) and returns the card for it, when it is a call or a video.
func cardLine(line string) (annCard, bool) {
	t := strings.TrimSpace(line)
	if t == "" {
		return annCard{}, false
	}
	if t[0] == '[' {
		if label, addr, n, ok := scanLabelledLink(t); ok && n == len(t) {
			return classifyAnnouncementCard(addr, label)
		}
		return annCard{}, false
	}
	if annBareURL.FindString(t) == t {
		if addr, ok := cleanAnnouncementURL(t); ok {
			return classifyAnnouncementCard(addr, "")
		}
	}
	return annCard{}, false
}

func parseAnnouncement(body string) []annBlock {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	var blocks []annBlock
	var open *annBlock
	closeOpen := func() {
		if open != nil {
			blocks = append(blocks, *open)
			open = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			closeOpen()
			continue
		}
		if item, ok := strings.CutPrefix(strings.TrimLeft(line, " \t"), "- "); ok && strings.TrimSpace(item) != "" {
			if open == nil || open.kind != "ul" {
				closeOpen()
				open = &annBlock{kind: "ul"}
			}
			open.lines = append(open.lines, parseAnnouncementInline(strings.TrimSpace(item), true))
			continue
		}
		if card, ok := cardLine(line); ok {
			closeOpen()
			blocks = append(blocks, annBlock{kind: "card", card: card})
			continue
		}
		if open == nil || open.kind != "p" {
			closeOpen()
			open = &annBlock{kind: "p"}
		}
		open.lines = append(open.lines, parseAnnouncementInline(strings.TrimSpace(line), true))
	}
	closeOpen()
	return blocks
}

// ---- web ----

const ytThumb = "https://i.ytimg.com/vi/"

func inlineHTML(parts []annInline, b *strings.Builder) {
	for _, p := range parts {
		switch p.kind {
		case 't':
			b.WriteString(html.EscapeString(p.text))
		case 'b':
			b.WriteString("<strong>")
			inlineHTML(p.children, b)
			b.WriteString("</strong>")
		case 'l':
			b.WriteString(`<a href="` + html.EscapeString(p.url) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(p.text) + `</a>`)
		}
	}
}

func linesHTML(lines [][]annInline, b *strings.Builder) {
	for i, l := range lines {
		if i > 0 {
			b.WriteString("<br>")
		}
		inlineHTML(l, b)
	}
}

const annCallIcon = `<svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="6" width="12" height="12" rx="2.5"/><path d="m15 10.5 6-3.5v10l-6-3.5"/></svg>`

// cardTitle is the card's main line: the organiser's label, or the service
// name alone when the address had no label.
func cardTitle(c annCard) string {
	if c.label != "" {
		return c.label
	}
	return c.service
}

// cardService is the small line above a labelled card's title; an unlabelled
// card already shows the service as its title.
func cardService(c annCard) string {
	if c.label == "" {
		return ""
	}
	return c.service
}

// AnnouncementHTML renders announcement text for the web: paragraphs, bullet
// lists, links and cards, all escaped. The caller wraps it in its own container.
func AnnouncementHTML(body string) string {
	var b strings.Builder
	for _, bl := range parseAnnouncement(body) {
		switch bl.kind {
		case "p":
			b.WriteString("<p>")
			linesHTML(bl.lines, &b)
			b.WriteString("</p>")
		case "ul":
			b.WriteString("<ul>")
			for _, l := range bl.lines {
				b.WriteString("<li>")
				inlineHTML(l, &b)
				b.WriteString("</li>")
			}
			b.WriteString("</ul>")
		case "card":
			c := bl.card
			kind := "ann-call"
			lead := `<span class="ann-card-ico">` + annCallIcon + `</span>`
			if c.videoID != "" {
				kind = "ann-video"
				lead = `<span class="ann-card-thumb"><img src="` + ytThumb + html.EscapeString(c.videoID) + `/hqdefault.jpg" alt="" loading="lazy" referrerpolicy="no-referrer"></span>`
			}
			b.WriteString(`<a class="ann-card ` + kind + `" href="` + html.EscapeString(c.url) + `" target="_blank" rel="noopener noreferrer">` +
				lead + `<span class="ann-card-text">`)
			if svc := cardService(c); svc != "" {
				b.WriteString(`<span class="ann-card-svc">` + html.EscapeString(svc) + `</span>`)
			}
			b.WriteString(`<span class="ann-card-label">` +
				html.EscapeString(cardTitle(c)) + `</span></span><span class="ann-card-btn">` + html.EscapeString(c.action) + `</span></a>`)
		}
	}
	return b.String()
}

// ---- email ----

const (
	emailFont   = `font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;`
	emailAccent = "#a33f31"
)

func inlineEmailHTML(parts []annInline, b *strings.Builder) {
	for _, p := range parts {
		switch p.kind {
		case 't':
			b.WriteString(html.EscapeString(p.text))
		case 'b':
			b.WriteString("<strong>")
			inlineEmailHTML(p.children, b)
			b.WriteString("</strong>")
		case 'l':
			b.WriteString(`<a href="` + html.EscapeString(p.url) + `" style="color:` + emailAccent + `;text-decoration:underline;">` + html.EscapeString(p.text) + `</a>`)
		}
	}
}

// AnnouncementEmailHTML renders announcement text for the HTML part of an
// email: inline styles only, and a table-based button for each call or video.
func AnnouncementEmailHTML(body string) string {
	var b strings.Builder
	para := `margin:0 0 14px;` + emailFont + `font-size:15px;line-height:1.55;color:#1a1a1a;`
	for _, bl := range parseAnnouncement(body) {
		switch bl.kind {
		case "p":
			b.WriteString(`<p style="` + para + `">`)
			for i, l := range bl.lines {
				if i > 0 {
					b.WriteString("<br>")
				}
				inlineEmailHTML(l, &b)
			}
			b.WriteString("</p>")
		case "ul":
			b.WriteString(`<ul style="margin:0 0 14px;padding:0 0 0 22px;` + emailFont + `font-size:15px;line-height:1.55;color:#1a1a1a;">`)
			for _, l := range bl.lines {
				b.WriteString(`<li style="margin:0 0 4px;">`)
				inlineEmailHTML(l, &b)
				b.WriteString("</li>")
			}
			b.WriteString("</ul>")
		case "card":
			c := bl.card
			svcLine := ""
			if svc := cardService(c); svc != "" {
				svcLine = `<div style="font-size:12px;letter-spacing:.04em;text-transform:uppercase;color:#615b51;margin:0 0 3px;">` + html.EscapeString(svc) + `</div>`
			}
			b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="margin:0 0 16px;border:1px solid #e2d9c6;border-radius:10px;background:#fffdf7;"><tr><td style="padding:16px 18px;` + emailFont + `">` +
				svcLine +
				`<div style="font-size:16px;font-weight:600;color:#1a1a1a;margin:0 0 14px;word-break:break-word;">` + html.EscapeString(cardTitle(c)) + `</div>` +
				`<table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr><td align="center" bgcolor="` + emailAccent + `" style="border-radius:8px;background:` + emailAccent + `;">` +
				`<a href="` + html.EscapeString(c.url) + `" style="display:inline-block;padding:12px 22px;` + emailFont + `font-size:15px;font-weight:600;line-height:1;color:#ffffff;text-decoration:none;border-radius:8px;">` + html.EscapeString(c.action) + `</a>` +
				`</td></tr></table></td></tr></table>`)
		}
	}
	return b.String()
}

// AnnouncementText is the text part of an email: the lines the organiser
// wrote, with each labelled link written as "label (address)" and bold marks
// removed, so every address is visible in full.
func AnnouncementText(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		var b strings.Builder
		inlineText(parseAnnouncementInline(line, true), &b)
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n")
}

func inlineText(parts []annInline, b *strings.Builder) {
	for _, p := range parts {
		switch p.kind {
		case 't':
			b.WriteString(p.text)
		case 'b':
			inlineText(p.children, b)
		case 'l':
			if p.text == p.url || strings.EqualFold(strings.TrimRight(p.text, "/"), strings.TrimRight(p.url, "/")) {
				b.WriteString(p.url)
			} else {
				b.WriteString(p.text + " (" + p.url + ")")
			}
		}
	}
}
