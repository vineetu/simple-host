package gate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"html"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// passcodeGateCSS is the gate's only style; its hash is in the page's CSP.
// It draws the same card as the sign-in mock (ink colours, wobble, Caveat
// when the host status font is on the page). Selectors beat host-status.css,
// which HostedStatusPage appends after this block. No page theme of its own:
// light and dark follow html[data-theme] from the shared theme script.
const passcodeGateCSS = `html:root{color-scheme:light}html:root[data-theme=dark]{color-scheme:dark}` +
	`html:root body{margin:0;min-height:100vh;padding:36px 16px 48px;background:#f6f4e9;color:#103f49;font:16px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;text-align:left}` +
	`html:root[data-theme=dark] body{background:#0b1222;color:#f6f4e9}` +
	`html:root main.gate{box-sizing:border-box;width:100%;max-width:400px;margin:0 auto;padding:28px 26px 22px;background:#fffdf5;color:#103f49;border:1.5px solid #45646a;border-radius:255px 18px 225px 18px / 18px 225px 18px 255px;box-shadow:2px 3px 0 #c9d7cd}` +
	`html:root[data-theme=dark] main.gate{background:#172332;color:#f6f4e9;border-color:#334155;box-shadow:2px 3px 0 #020617}` +
	`html:root main.gate *{box-sizing:border-box}` +
	`html:root main.gate .eyebrow{font:600 12px/1.4 system-ui,sans-serif;letter-spacing:.04em;text-transform:uppercase;color:#526c70;margin:0 0 6px;overflow-wrap:anywhere}` +
	`html:root[data-theme=dark] main.gate .eyebrow{color:#adc5c2}` +
	`html:root main.gate h1{font-family:Caveat,"Segoe Print","Bradley Hand",cursive;font-size:40px;font-weight:700;line-height:1.15;margin:0 0 6px;color:inherit;text-align:left}` +
	`html:root main.gate .lead{margin:0 0 20px;color:#45646a;font-size:15.5px;line-height:1.5}` +
	`html:root main.gate .lead b{color:#103f49;font-weight:700;overflow-wrap:anywhere}` +
	`html:root[data-theme=dark] main.gate .lead{color:#c4d9d0}` +
	`html:root[data-theme=dark] main.gate .lead b{color:#f6f4e9}` +
	`html:root main.gate label{display:block;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;font-weight:600;font-size:14px;color:inherit;margin:0 0 6px;text-align:left}` +
	`html:root main.gate input{display:block;width:100%;max-width:100%;height:48px;min-height:44px;padding:0 14px;margin:0;background:#f6f4e9;color:#103f49;border:1.5px solid #c9d7cd;border-radius:10px;font:16px system-ui,-apple-system,"Segoe UI",sans-serif;text-align:left}` +
	`html:root[data-theme=dark] main.gate input{background:#0b1222;color:#f6f4e9;border-color:#334155}` +
	`html:root main.gate input.code{height:60px;text-align:center;font:600 30px/1 ui-monospace,monospace;letter-spacing:.42em;padding-left:.42em}` +
	`html:root main.gate input.bad{border-color:#a33f31}` +
	`html:root[data-theme=dark] main.gate input.bad{border-color:#f39da0}` +
	`html:root main.gate button.primary{display:block;width:100%;height:48px;margin-top:12px;border:0;border-radius:255px 18px 225px 18px / 18px 225px 18px 255px;background:#116b77;color:#fffdf5;font-family:Caveat,"Segoe Print",cursive;font-size:22px;font-weight:700;cursor:pointer}` +
	`html:root[data-theme=dark] main.gate button.primary{background:#9edacf;color:#0b1222}` +
	`html:root main.gate button.secondary{display:block;width:100%;height:46px;margin-top:10px;border:1.5px solid #c9d7cd;border-radius:255px 18px 225px 18px / 18px 225px 18px 255px;background:#fffdf5;color:#103f49;font-family:Caveat,"Segoe Print",cursive;font-size:20px;cursor:pointer}` +
	`html:root[data-theme=dark] main.gate button.secondary{background:#172332;color:#f6f4e9;border-color:#334155}` +
	`html:root main.gate button[disabled]{opacity:.55;cursor:default}` +
	`html:root main.gate a.gbtn{display:flex;align-items:center;justify-content:center;gap:10px;width:100%;height:46px;border-radius:23px;background:#fff;border:1px solid #747775;color:#1f1f1f;font:500 15px/20px Roboto,system-ui,-apple-system,"Segoe UI",sans-serif;letter-spacing:.1px;text-decoration:none;padding:0 12px}` +
	`html:root main.gate a.gbtn svg{width:20px;height:20px;flex:none}` +
	`html:root[data-theme=dark] main.gate a.gbtn{background:#131314;border-color:#8e918f;color:#e3e3e3}` +
	`html:root main.gate .or{display:flex;align-items:center;gap:12px;margin:18px 0;color:#526c70;font-size:14px}` +
	`html:root main.gate .or::before,html:root main.gate .or::after{content:"";flex:1;border-top:1px solid #c9d7cd}` +
	`html:root[data-theme=dark] main.gate .or{color:#adc5c2}` +
	`html:root[data-theme=dark] main.gate .or::before,html:root[data-theme=dark] main.gate .or::after{border-top-color:#334155}` +
	`html:root main.gate .fine{margin:16px 0 0;font-size:13px;line-height:1.5;color:#526c70}` +
	`html:root[data-theme=dark] main.gate .fine{color:#adc5c2}` +
	`html:root main.gate .err{display:flex;gap:8px;align-items:flex-start;margin:8px 0 0;color:#a33f31;font-size:14px;line-height:1.45;text-align:left}` +
	`html:root main.gate .err svg{flex:none;width:16px;height:16px;margin-top:2px}` +
	`html:root[data-theme=dark] main.gate .err{color:#f39da0}` +
	`html:root main.gate .who{display:flex;align-items:center;gap:10px;padding:10px 12px;border:1px solid #c9d7cd;border-radius:10px;background:#f6f4e9;margin:0 0 16px;font-size:14.5px}` +
	`html:root main.gate .who .av{width:30px;height:30px;border-radius:50%;background:#e7f2f3;color:#116b77;display:grid;place-items:center;font-weight:700;flex:none}` +
	`html:root main.gate .who b{overflow-wrap:anywhere}` +
	`html:root[data-theme=dark] main.gate .who{background:#0b1222;border-color:#334155}` +
	`html:root[data-theme=dark] main.gate .who .av{background:#134e4a;color:#9edacf}` +
	`html:root main.gate .row{display:flex;justify-content:space-between;align-items:center;gap:10px;margin-top:14px;flex-wrap:wrap}` +
	`html:root main.gate button.link,html:root main.gate a.link{display:inline-flex;align-items:center;background:none;border:0;padding:0;margin:0;color:#116b77;font:14px/1.4 system-ui,sans-serif;text-decoration:underline;min-height:44px;cursor:pointer;border-radius:0;box-shadow:none}` +
	`html:root[data-theme=dark] main.gate button.link,html:root[data-theme=dark] main.gate a.link{color:#9edacf}` +
	`html:root main.gate form{margin:0}` +
	`html:root main.gate form.inline{display:inline}` +
	`@media (max-width:360px){html:root main.gate h1{font-size:34px}html:root main.gate input.code{font-size:22px;letter-spacing:.24em;padding-left:.24em}}`

// gateAssistJS runs only on the private-site code page. It submits the code
// form once six digits are in the box, and holds "Send a new code" for 30s
// after a send. Its hash is added to the gate CSP; there is no other script.
const gateAssistJS = `(function(){var root=document.querySelector("main.gate");var code=document.querySelector("input[name=code]");if(code&&code.form){var sent=false;code.addEventListener("input",function(){var d=String(code.value||"").replace(/\D/g,"").slice(0,6);if(code.value!==d)code.value=d;if(d.length===6&&!sent){sent=true;code.form.submit();}});}var btn=document.querySelector("[data-sh-resend]");if(!btn||!root)return;var fresh=root.getAttribute("data-sh-sent")==="1";var key="sh-gate-resend";var now=Date.now();var until=0;try{until=parseInt(sessionStorage.getItem(key)||"0",10)||0;}catch(e){}if(fresh){until=now+30000;try{sessionStorage.setItem(key,String(until));}catch(e){}}else if(!until||until<=now){return;}function tick(){var left=Math.ceil((until-Date.now())/1000);if(left<=0){btn.disabled=false;btn.textContent="Send a new code";return;}btn.disabled=true;btn.textContent="Send a new code in 0:"+(left<10?"0":"")+left;setTimeout(tick,1000);}tick();})();`

const gateAssistTag = "<script>" + gateAssistJS + "</script>"

const gateInfoSVG = `<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="1.6"/><path d="M8 4.2v4.6M8 10.9v.9" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"/></svg>`

const gateGoogleSVG = `<svg viewBox="0 0 48 48" width="20" height="20" aria-hidden="true"><path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z"/><path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z"/><path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z"/><path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z"/></svg>`

// passcodeGateHead is the gate's head with the site-wide theme script
// (partials/theme.html, like the offline page), built once.
var passcodeGateHead = web.Themed(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow"><meta name="referrer" content="same-origin"><!--sh:theme-->
<title>Protected site</title><meta property="og:title" content="Protected site">
<style>` + passcodeGateCSS + `</style></head>`)

var passcodeGateCSP = func() string {
	css := sha256.Sum256([]byte(passcodeGateCSS))
	csp := "default-src 'none'; style-src 'sha256-" + base64.StdEncoding.EncodeToString(css[:]) + "'"
	var hashes []string
	if i := strings.Index(passcodeGateHead, "<script>"); i >= 0 {
		body := passcodeGateHead[i+len("<script>"):]
		body = body[:strings.Index(body, "</script>")]
		js := sha256.Sum256([]byte(body))
		hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(js[:])+"'")
	}
	assist := sha256.Sum256([]byte(gateAssistJS))
	hashes = append(hashes, "'sha256-"+base64.StdEncoding.EncodeToString(assist[:])+"'")
	csp += "; script-src " + strings.Join(hashes, " ")
	return csp + "; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
}()

// Host's self-contained handwriting changes only font/style sources. The
// gate's script hash, form action and every access rule are unchanged.
var hostPasscodeGateCSP = func() string {
	css := sha256.Sum256([]byte(web.HostStatusCSS))
	return strings.Replace(passcodeGateCSP, "style-src ", "style-src 'sha256-"+base64.StdEncoding.EncodeToString(css[:])+"' ", 1) + "; font-src data:"
}()

// servePasscodeGate writes the protected page: 401, never stored, never
// indexed, nothing of the site in it (no title, no owner, no preview image;
// the only name on it is the host already in the address bar). next ""
// leaves the form out (a shared host, where no passcode is entered). A
// request that is not a page (a script, an image, a fetch) gets a short
// plain 401. /robots.txt answers "Disallow: /" with 200, so crawlers stop
// asking.
func (h *Handler) servePasscodeGate(w http.ResponseWriter, r *http.Request, next, msg string, retryAfter time.Duration) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Del("Vary")
	w.Header().Del("Last-Modified")
	w.Header().Del("ETag")
	if r.URL.Path == "/robots.txt" && retryAfter == 0 && msg == "" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /\n"))
		}
		return
	}
	status := http.StatusUnauthorized
	if retryAfter > 0 {
		status = http.StatusTooManyRequests
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
	}
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte("This site is protected.\n"))
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", passcodeGateCSP)
	if !h.Pipeline().ChromeProduct {
		w.Header().Set("Content-Security-Policy", hostPasscodeGateCSP)
	}
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(h.passcodeGateHTML(httpx.HostName(r), next, msg)))
}

func (h *Handler) passcodeGateHTML(host, next, msg string) string {
	var b strings.Builder
	if h.Pipeline().ChromeProduct {
		b.WriteString(passcodeGateHead)
	} else {
		b.Write(h.Pipeline().HostedStatusPage(passcodeGateHead))
	}
	b.WriteString(`<body><main class="gate">`)
	b.WriteString(`<p class="eyebrow">` + html.EscapeString(host) + `</p>`)
	b.WriteString(`<h1>Enter the passcode</h1>`)
	if next == "" {
		b.WriteString(`<p class="lead">Open it at its own address to enter the passcode.</p>`)
	} else {
		b.WriteString(`<p class="lead">The owner protected this site with a passcode. They will have given it to you.</p>`)
		b.WriteString(`<form method="post" action="/v1/site-unlock"><input type="hidden" name="next" value="` + html.EscapeString(next) + `">`)
		b.WriteString(`<label for="sh-pass">Passcode</label>`)
		bad := ""
		if msg != "" {
			bad = ` class="bad" aria-describedby="sh-pass-err"`
		}
		b.WriteString(`<input id="sh-pass" name="passcode" type="password" aria-label="Passcode" autocomplete="current-password" autofocus required maxlength="1024"` + bad + `>`)
		if msg != "" {
			b.WriteString(`<p class="err" id="sh-pass-err" role="alert">` + gateInfoSVG + `<span>` + html.EscapeString(msg) + `</span></p>`)
		}
		b.WriteString(`<button type="submit" class="primary">Open site</button></form>`)
	}
	b.WriteString(`</main></body></html>
`)
	return b.String()
}

// passcodeSiteForHost finds the protected site a visitor is unlocking: the
// one the host names (its site host, custom domain or claimed name), or on
// a person host the one the path's first segment names. Never on a shared
// host.
func (h *Handler) passcodeSiteForHost(ctx context.Context, host, next string) (userID, name string, ok bool, err error) {
	if host == "" || h.Hosts.IsSharedHost(host) {
		return "", "", false, nil
	}
	if h.Hosts.IsSiteHost(ctx, host) {
		site, _, ok, err := h.Hosts.SiteHostSite(ctx, host)
		if err != nil || !ok {
			return "", "", false, err
		}
		return site.UserID, site.Name, true, nil
	}
	if owner, onPerson := h.Hosts.PersonHostOwner(ctx, host); onPerson {
		if !h.Ed.Storage().EventSites {
			home, has, err := h.SitesSvc().HomeSite(ctx, owner.ID)
			if err != nil {
				return "", "", false, err
			}
			if has {
				return home.UserID, home.Name, home.Live(), nil
			}
		}
		seg, _, _ := strings.Cut(strings.TrimPrefix(next, "/"), "/")
		seg, _, _ = strings.Cut(seg, "?")
		site, err := h.SitesSvc().AddressedSite(ctx, owner.ID, seg)
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", false, nil
		}
		if err != nil {
			return "", "", false, err
		}
		return site.UserID, site.Name, true, nil
	}
	// A family address names its site (a custom domain bound under the
	// family is not one: familySiteForHost leaves it to the lookup below).
	if m, ok, err := h.Hosts.FamilySiteForHost(ctx, host); err != nil {
		return "", "", false, err
	} else if ok {
		return m.Site.UserID, m.Site.Name, true, nil
	}
	info, err := h.SitesSvc().GetSiteByCustomDomain(ctx, host)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return info.UserID, info.Name, true, nil
}
