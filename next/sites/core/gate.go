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

var gatePage = template.Must(template.New("gate").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex"><meta name="theme-color" content="#f6f4e9"><title>{{.Title}}</title>
<style>` + gateCSS + `</style></head><body class="sh-gate">
<p class="sh-mark" aria-label="simple-host"><span class="a">simple</span><i>-</i>host</p>
<main class="sh-card{{if .Plain}} plain{{end}}">
{{if not .Plain}}<p class="sh-eyebrow">{{.Host}}</p>{{end}}
<h1>{{.Title}}</h1>
{{if and (eq .D "ask_access") .Email}}<div class="sh-who"><span class="sh-avatar" aria-hidden="true">{{slice .Email 0 1}}</span><span>Signed in as <b>{{.Email}}</b></span></div>{{end}}
<p class="sh-lead">{{.Text}}</p>
{{if eq .D "passcode"}}<form method="post" action="/_sh/passcode"><input type="hidden" name="next" value="{{.Next}}">
<label for="sh-pass">Passcode</label>
<input id="sh-pass" name="passcode" type="password" autocomplete="current-password" required autofocus class="sh-input{{if .Note}} bad{{end}}"{{if .Note}} aria-invalid="true" aria-describedby="sh-msg"{{end}}>
{{template "msg" .}}
<button class="sh-btn" type="submit">Open site</button></form>
{{else if eq .D "sign_in"}}<a class="sh-btn" href="/_sh/signin?return={{.Next}}">Sign in</a>
<p class="sh-fine">You sign in on simple-host with Google or a 6-digit code we email you. No password.</p>
{{else if eq .D "ask_access"}}{{template "msg" .}}{{if not .Good}}<form method="post" action="/_sh/ask-access"><input type="hidden" name="next" value="{{.Next}}">
<button class="sh-btn" type="submit">Ask for access</button></form>{{end}}
{{else}}{{template "msg" .}}{{end}}
</main></body></html>
{{define "msg"}}{{if .Note}}<p class="sh-msg {{if .Good}}good{{else}}bad{{end}}" id="sh-msg" role="{{if .Good}}status{{else}}alert{{end}}">{{if .Good}}` + gateTick + `{{else}}` + gateInfo + `{{end}}<span>{{.Note}}</span></p>{{end}}{{end}}`))

var gateText = map[Decision]struct {
	status      int
	title, text string
}{
	TakenDown:  {http.StatusGone, "This site has been taken down", "It is no longer available."},
	Offline:    {503, "This site is offline", "Its owner has taken it offline for now."},
	NeedCode:   {401, "Enter the passcode", "The owner protected this site with a passcode. They will have given it to you."},
	NeedSignIn: {401, "This site is private", "Only people the owner named can open it. Sign in with the email they added."},
	AskAccess:  {403, "You do not have access", "Your email is not on the owner's list. Ask for access and they will be told."},
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
		"Next": Next(r), "Host": web.Host(r), "Email": web.CallerOf(r).Email, "Plain": d == TakenDown || d == Offline})
}

// NotFoundPage is what a browser sees at a site address that names no site.
func NotFoundPage(w http.ResponseWriter, r *http.Request) {
	writeGate(w, http.StatusNotFound, map[string]any{"D": "not_found", "Title": "That site isn’t here",
		"Text": "Nothing is published at " + web.Host(r) + ". Check the address for a typo.", "Plain": true})
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
