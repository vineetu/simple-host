package sites

import (
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"

	"simplehost/core/web"
	"simplehost/web/dist"
)

// gateCSS is the shared page stylesheet (built from the same design tokens as
// the owner app, see web/app) plus the hand font, inline: a site address
// loads nothing from the platform. The inline font face comes last, so it
// wins over the stylesheet's own (platform) address for it.
var gateCSS = dist.PagesCSS + `@font-face{font-family:ShCaveat;src:url(data:font/woff2;base64,` +
	base64.StdEncoding.EncodeToString([]byte(dist.CaveatFont)) + `) format("woff2");font-weight:400 700;font-display:swap}`

const gateInfo = `<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M8 4.2v4.6M8 10.9v.9" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>`
const gateTick = `<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M4.8 8.3l2.2 2.2 4.2-4.6" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`

// gateIcons is the line drawing at the top of each page's card (24 px grid).
var gateIcons = map[string]template.HTML{
	"passcode":   `<rect x="5" y="10.5" width="14" height="10" rx="2"/><path d="M8.5 10.5V7.5a3.5 3.5 0 0 1 7 0v3M12 14.5v2.5"/>`,
	"sign_in":    `<circle cx="12" cy="8.5" r="3.5"/><path d="M5 20c.8-3.6 3.6-5.5 7-5.5s6.2 1.9 7 5.5"/>`,
	"ask_access": `<circle cx="10" cy="8.5" r="3.5"/><path d="M3.5 20c.7-3.4 3.2-5.5 6.5-5.5 1.3 0 2.5.3 3.5 1M18 14v6M15 17h6"/>`,
	"offline":    `<circle cx="12" cy="12" r="8.5"/><path d="M10 9v6M14 9v6"/>`,
	"taken_down": `<circle cx="12" cy="12" r="8.5"/><path d="M6 6l12 12"/>`,
	"not_found":  `<circle cx="11" cy="11" r="6.5"/><path d="M16 16l4.5 4.5M9.2 9.3a2 2 0 1 1 2.6 2c-.5.2-.8.6-.8 1.1M11 14.6v.1"/>`,
}

var gatePage = template.Must(template.New("gate").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex"><meta name="theme-color" content="#f6f4e9"><title>{{.Title}}</title>
<style>` + gateCSS + `</style></head><body class="sh-gate">
<main class="sh-card sh-gate-card">
<div class="sh-gate-icon{{if or (eq .D "taken_down") (eq .D "not_found")}} quiet{{end}}"><svg viewBox="0 0 24 24" aria-hidden="true">{{.Icon}}</svg></div>
{{if .Host}}<p class="sh-site">{{.Host}}</p>{{end}}
<h1>{{.Title}}</h1>
{{if and (eq .D "ask_access") .Email}}<div class="sh-who"><span class="sh-avatar" aria-hidden="true">{{slice .Email 0 1}}</span><span>Signed in as <b>{{.Email}}</b></span></div>{{end}}
<p class="sh-lead">{{.Text}}</p>
{{if eq .D "passcode"}}<form method="post" action="/_sh/passcode"><input type="hidden" name="next" value="{{.Next}}">
<label for="sh-pass">Passcode</label>
<input id="sh-pass" name="passcode" type="password" autocomplete="current-password" required autofocus class="sh-input{{if .Note}} bad{{end}}"{{if .Note}} aria-invalid="true" aria-describedby="sh-msg"{{end}}>
{{template "msg" .}}
<button class="sh-btn" type="submit">Open site</button></form>
<p class="sh-fine">No passcode? Ask the person who sent you the link.</p>
{{else if eq .D "sign_in"}}<a class="sh-btn" href="/_sh/signin?return={{.Next}}">Sign in</a>
<p class="sh-fine">Sign in with Google or with a 6-digit code we email you. There is no password.</p>
{{else if eq .D "ask_access"}}{{template "msg" .}}{{if not .Good}}<form method="post" action="/_sh/ask-access"><input type="hidden" name="next" value="{{.Next}}">
<button class="sh-btn" type="submit">Ask for access</button></form>{{end}}
{{if .Email}}<form method="post" action="/_sh/signout"><input type="hidden" name="next" value="{{.Next}}">
<button class="sh-btn secondary" type="submit">Sign out</button></form>
<p class="sh-fine">Invited with another email? Sign out, then sign in with that one.</p>{{end}}
{{else}}{{template "msg" .}}{{end}}
</main>
<p class="sh-by">Hosted on <span class="sh-mark"><span class="a">simple</span><i>-</i>host</span></p>
</body></html>
{{define "msg"}}{{if .Note}}<p class="sh-msg {{if .Good}}good{{else}}bad{{end}}" id="sh-msg" role="{{if .Good}}status{{else}}alert{{end}}">{{if .Good}}` + gateTick + `{{else}}` + gateInfo + `{{end}}<span>{{.Note}}</span></p>{{end}}{{end}}`))

var gateText = map[Decision]struct {
	status      int
	title, text string
}{
	TakenDown:  {http.StatusGone, "This site has been taken down", "It is no longer available at this address."},
	Offline:    {503, "This site is offline", "Its owner has taken it offline for now. Try again later."},
	NeedCode:   {401, "Enter the passcode", "This site is locked. Type the passcode its owner gave you to open it."},
	NeedSignIn: {401, "This site is private", "Only people its owner invited can open it. Sign in with the email they invited."},
	AskAccess:  {403, "You do not have access", "This email is not on the owner's list. Ask for access and we will tell the owner."},
}

// GatePage answers a visit the access decision did not open, as a small
// page: taken down, offline, the passcode form, sign in, or Ask for access.
// status 0 means the decision's own; note is one more line, such as a wrong
// passcode (a 200 note is good news, such as "we told the owner").
func GatePage(w http.ResponseWriter, r *http.Request, d Decision, status int, note string) {
	t := gateText[d]
	if status == 0 {
		status = t.status
	}
	writeGate(w, status, map[string]any{"D": string(d), "Title": t.title, "Text": t.text, "Note": note, "Good": status == http.StatusOK,
		"Next": Next(r), "Host": web.Host(r), "Email": web.CallerOf(r).Email, "Icon": gateIcons[string(d)]})
}

// NotFoundPage is what a browser sees at a site address that names no site.
func NotFoundPage(w http.ResponseWriter, r *http.Request) {
	writeGate(w, http.StatusNotFound, map[string]any{"D": "not_found", "Title": "That site isn’t here",
		"Text": "Nothing is published at this address. Check it for a typo, or ask whoever sent you the link.",
		"Host": web.Host(r), "Icon": gateIcons["not_found"]})
}

func writeGate(w http.ResponseWriter, status int, v map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	gatePage.Execute(w, v)
}

// Next is where to go back to after a gate form: the page asked for, or the
// form's own next field when it is a path on this host.
func Next(r *http.Request) string {
	next := r.URL.RequestURI()
	if r.Method == http.MethodPost {
		next = r.PostFormValue("next")
	}
	if !web.LocalPath(next) || strings.HasPrefix(next, "/_sh/") {
		return "/"
	}
	return next
}
