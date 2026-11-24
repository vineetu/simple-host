package sites

import (
	"html/template"
	"net/http"
	"strings"
)

var gatePage = template.Must(template.New("gate").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Title}}</title>
<style>body{margin:0;background:#fafafa;color:#1a1a1a;font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:26rem;margin:15vh auto;padding:0 16px}h1{font-size:1.3rem;margin:0 0 .5rem}p{margin:.5rem 0}
.note{color:#9a3412}input,button{font:inherit;padding:.55rem .7rem;border-radius:8px;border:1px solid #d4d4d4}
input{width:100%;box-sizing:border-box;margin:.5rem 0}button{background:#1a1a1a;color:#fff;border-color:#1a1a1a;cursor:pointer}
a{color:inherit}</style></head><body><main>
<h1>{{.Title}}</h1><p>{{.Text}}</p>{{if .Note}}<p class="note">{{.Note}}</p>{{end}}
{{if eq .D "passcode"}}<form method="post" action="/_sh/passcode"><input type="hidden" name="next" value="{{.Next}}">
<input name="passcode" type="password" autocomplete="current-password" aria-label="Passcode" required autofocus>
<button type="submit">Open</button></form>{{end}}
{{if eq .D "sign_in"}}<p><a href="/_sh/signin?next={{.Next}}">Sign in</a></p>{{end}}
{{if eq .D "ask_access"}}<form method="post" action="/_sh/ask-access"><input type="hidden" name="next" value="{{.Next}}">
<button type="submit">Ask for access</button></form>{{end}}
</main></body></html>`))

var gateText = map[Decision]struct {
	status      int
	title, text string
}{
	TakenDown:  {451, "This site was taken down", "The operator took this site down."},
	Offline:    {503, "This site is offline", "Its owner has taken it offline for now."},
	NeedCode:   {401, "This site has a passcode", "Enter the passcode its owner gave you."},
	NeedSignIn: {401, "This site is private", "Sign in to see it. Only people its owner named can open it."},
	AskAccess:  {403, "You are not on the list", "Its owner has not named you. Ask them for access and they will be told."},
}

// GatePage answers a visit the access decision did not open, as a small
// page: taken down, offline, the passcode form, sign in, or Ask for access.
// status 0 means the decision's own; note is one more line, such as a wrong
// passcode.
func GatePage(w http.ResponseWriter, r *http.Request, d Decision, status int, note string) {
	t := gateText[d]
	if status == 0 {
		status = t.status
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	gatePage.Execute(w, map[string]string{"D": string(d), "Title": t.title, "Text": t.text, "Note": note, "Next": Next(r)})
}

// Next is where to go back to after a gate form: the page asked for, or the
// form's own next field when it is a path on this host.
func Next(r *http.Request) string {
	next := r.URL.RequestURI()
	if r.Method == http.MethodPost {
		next = r.PostFormValue("next")
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.HasPrefix(next, "/_sh/") {
		return "/"
	}
	return next
}
