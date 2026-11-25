package sites

import (
	_ "embed"
	"encoding/base64"
	"html/template"
	"net/http"
	"strings"

	"simplehost/core/web"
)

// gateFont is Caveat Bold cut down to plain Latin, for the headings and
// buttons. It is inside the page because a site address may not load fonts
// from the platform.
//
//go:embed gate-caveat.woff2
var gateFont []byte

// gateCSS draws the old Simple Host card: paper, ink, a hand-drawn edge.
// Light only. One column on a phone, a centred card on a wide screen.
var gateCSS = `@font-face{font-family:ShCaveat;src:url(data:font/woff2;base64,` + base64.StdEncoding.EncodeToString(gateFont) + `) format("woff2");font-weight:700;font-display:swap}
:root{color-scheme:light;--paper:#f6f4e9;--card:#fffdf5;--ink:#103f49;--soft:#45646a;--faint:#526c70;--line:#c9d7cd;--teal:#116b77;--coral:#c0412b;--bad:#a33f31;--good:#2f6b3b;--hand:ShCaveat,"Segoe Print","Bradley Hand",cursive}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;min-height:100vh;min-height:100dvh;display:flex;flex-direction:column;align-items:center;background:var(--paper);color:var(--ink);font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
padding:max(28px,env(safe-area-inset-top)) max(16px,env(safe-area-inset-right)) max(32px,env(safe-area-inset-bottom)) max(16px,env(safe-area-inset-left))}
.mark{font:700 32px/1 var(--hand);margin:0 0 24px;letter-spacing:.01em}
.mark .a{color:var(--coral)}.mark .b{color:var(--teal)}.mark i{font-style:normal;color:var(--faint)}
main{width:100%;max-width:420px;margin:auto 0;padding:28px 24px 24px;background:var(--card);border:1.5px solid var(--soft);border-radius:255px 18px 225px 18px/18px 225px 18px 255px;box-shadow:2px 3px 0 var(--line)}
main.plain{background:none;border:0;box-shadow:none;text-align:center;max-width:560px}
.eyebrow{font:600 12px/1.4 system-ui,sans-serif;letter-spacing:.04em;text-transform:uppercase;color:var(--faint);margin:0 0 6px;overflow-wrap:anywhere}
h1{font:700 40px/1.15 var(--hand);margin:0 0 8px}
.lead{margin:0 0 20px;color:var(--soft);font-size:16px}
.plain .lead{margin:0}
label{display:block;font-weight:600;font-size:14px;margin:0 0 6px}
input{display:block;width:100%;height:48px;padding:0 14px;margin:0;background:var(--paper);color:var(--ink);border:1.5px solid var(--line);border-radius:10px;font:16px system-ui,-apple-system,"Segoe UI",sans-serif}
input:focus,.btn:focus-visible{outline:2px solid var(--teal);outline-offset:2px}
input.bad{border-color:var(--bad)}
.btn{display:flex;align-items:center;justify-content:center;width:100%;min-height:48px;margin:12px 0 0;padding:0 16px;border:0;border-radius:255px 18px 225px 18px/18px 225px 18px 255px;background:var(--teal);color:var(--card);font:700 23px/1 var(--hand);text-decoration:none;cursor:pointer;box-shadow:2px 3px 0 var(--line)}
.msg{display:flex;gap:8px;align-items:flex-start;margin:10px 0 0;font-size:14px;line-height:1.45}
.msg svg{flex:none;width:16px;height:16px;margin-top:2px}
.msg.bad{color:var(--bad)}.msg.good{color:var(--good)}
.who{display:flex;align-items:center;gap:10px;padding:10px 12px;border:1px solid var(--line);border-radius:10px;background:var(--paper);margin:0 0 16px;font-size:15px}
.who .av{width:30px;height:30px;border-radius:50%;background:#e7f2f3;color:var(--teal);display:grid;place-items:center;font-weight:700;flex:none;text-transform:uppercase}
.who b{overflow-wrap:anywhere}
.fine{margin:16px 0 0;font-size:13.5px;color:var(--faint)}
form{margin:0}
@media (min-width:700px){main{padding:36px 36px 30px}h1{font-size:44px}}`

const gateInfo = `<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M8 4.2v4.6M8 10.9v.9" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>`
const gateTick = `<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M4.8 8.3l2.2 2.2 4.2-4.6" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`

var gatePage = template.Must(template.New("gate").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta name="robots" content="noindex"><meta name="theme-color" content="#f6f4e9"><title>{{.Title}}</title>
<style>` + gateCSS + `</style></head><body>
<p class="mark" aria-label="simple-host"><span class="a">simple</span><i>-</i><span class="b">host</span></p>
<main{{if .Plain}} class="plain"{{end}}>
{{if not .Plain}}<p class="eyebrow">{{.Host}}</p>{{end}}
<h1>{{.Title}}</h1>
{{if and (eq .D "ask_access") .Email}}<div class="who"><span class="av">{{slice .Email 0 1}}</span><span>Signed in as <b>{{.Email}}</b></span></div>{{end}}
<p class="lead">{{.Text}}</p>
{{if eq .D "passcode"}}<form method="post" action="/_sh/passcode"><input type="hidden" name="next" value="{{.Next}}">
<label for="sh-pass">Passcode</label>
<input id="sh-pass" name="passcode" type="password" autocomplete="current-password" required autofocus{{if .Note}} class="bad" aria-describedby="sh-msg"{{end}}>
{{template "msg" .}}
<button class="btn" type="submit">Open site</button></form>
{{else if eq .D "sign_in"}}<a class="btn" href="/_sh/signin?return={{.Next}}">Sign in</a>
<p class="fine">You sign in on simple-host with Google or a 6-digit code we email you. No password.</p>
{{else if eq .D "ask_access"}}{{template "msg" .}}{{if not .Good}}<form method="post" action="/_sh/ask-access"><input type="hidden" name="next" value="{{.Next}}">
<button class="btn" type="submit">Ask for access</button></form>{{end}}
{{else}}{{template "msg" .}}{{end}}
</main></body></html>
{{define "msg"}}{{if .Note}}<p class="msg {{if .Good}}good{{else}}bad{{end}}" id="sh-msg" role="{{if .Good}}status{{else}}alert{{end}}">{{if .Good}}` + gateTick + `{{else}}` + gateInfo + `{{end}}<span>{{.Note}}</span></p>{{end}}{{end}}`))

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
