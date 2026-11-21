package web

import (
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

// Page helpers every module's routes answer with: the service-error page,
// the confirm-before-acting form for links opened from email, the
// preview-link markers.

// RenderServiceErrorPage is the branded 503 for a failure behind a page.
func RenderServiceErrorPage(w http.ResponseWriter, r *http.Request, pages *AssetPipeline) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(httpx.StampNonce(r, pages.HostedStatusPage(ServiceErrorPage)))
}

// ConfirmForm is a one-button POST form for a confirmation page: action is
// the path it posts to, fields its hidden inputs (escaped here).
func ConfirmForm(action string, fields map[string]string, button string) string {
	var b strings.Builder
	b.WriteString(`<form method="post" action="` + html.EscapeString(action) + `">`)
	for k, v := range fields {
		b.WriteString(`<input type="hidden" name="` + html.EscapeString(k) + `" value="` + html.EscapeString(v) + `">`)
	}
	b.WriteString(`<button type="submit" class="btn-primary" style="border:0;cursor:pointer;font:inherit">` + html.EscapeString(button) + `</button></form>`)
	return b.String()
}

// FragmentLinkGET answers the GET of an emailed link that carries its token
// in the fragment (no ?t=): the page that reads it and posts it back. It
// reports whether it wrote the response.
func FragmentLinkGET(assets *AssetPipeline, w http.ResponseWriter, r *http.Request, base, action string) bool {
	if r.Method == http.MethodPost || r.URL.Query().Get("t") != "" {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	nonce, _ := r.Context().Value(httpx.NonceKey{}).(string)
	attr := ""
	if nonce != "" {
		attr = ` nonce="` + html.EscapeString(nonce) + `"`
	}
	form := `<form id="sh-link" method="post" action="` + html.EscapeString(action) + `">` +
		`<input type="hidden" name="t" value=""><input type="hidden" name="peek" value="1">` +
		`<button type="submit" class="btn-primary" style="border:0;cursor:pointer;font:inherit">Continue</button></form>` +
		`<script` + attr + `>(function(){var m=/[#&]t=([^&]+)/.exec(location.hash||"");var f=document.getElementById("sh-link");` +
		`if(!m){f.hidden=true;return;}f.elements.t.value=decodeURIComponent(m[1]);` +
		`try{history.replaceState(null,"",location.pathname);}catch(e){}f.submit();})();</script>`
	assets.WriteMessagePage(w, r, base, http.StatusOK, "Opening your link…",
		"If nothing happens, press Continue. If there is no button, open the link from the email again.", "", "", form)
	return true
}

// LinkConfirming reports whether r only asks for the confirmation page: a GET
// (an older ?t= link) or the fragment page's POST with peek=1.
func LinkConfirming(r *http.Request) bool {
	return r.Method != http.MethodPost || r.PostFormValue("peek") == "1"
}

// linkToken is an emailed link's token: from the posted form on POST, from
// the query on GET (older links).
func LinkToken(w http.ResponseWriter, r *http.Request) string {
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		return strings.TrimSpace(r.PostFormValue("t"))
	}
	return strings.TrimSpace(r.URL.Query().Get("t"))
}

// PreviewPathSegment is the first path segment of a preview on a site host
// (the second, after the site name, on a person host).
const PreviewPathSegment = "__preview"

// PreviewReferer reports whether a request was made by a page opened as a
// preview on this same host (its Referer path is a preview path).
func PreviewReferer(r *http.Request) bool {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil || !strings.EqualFold(u.Hostname(), httpx.HostName(r)) {
		return false
	}
	p := strings.TrimPrefix(u.Path, "/")
	if strings.HasPrefix(p, PreviewPathSegment+"/") {
		return true
	}
	_, rest, _ := strings.Cut(p, "/")
	return strings.HasPrefix(rest, PreviewPathSegment+"/")
}

// OpenPreviewToken opens a preview link's token sealed with key: the site,
// the version, while it is within its lifetime ttl.
func OpenPreviewToken(key []byte, ttl time.Duration, token string, now time.Time) (siteID string, version int, err error) {
	raw, err := httpx.OpenLink(key, PreviewLinkDomain, token)
	if err != nil {
		return "", 0, errPreviewLink
	}
	payload := string(raw)
	parts := strings.Split(payload, ".")
	if len(parts) != 3 {
		return "", 0, errPreviewLink
	}
	v, err := strconv.Atoi(parts[1])
	exp, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || err2 != nil || v < 1 {
		return "", 0, errPreviewLink
	}
	left := time.Unix(exp, 0).Sub(now)
	if left <= 0 || left > ttl {
		return "", 0, errPreviewLink
	}
	return parts[0], v, nil
}

// AdminUICSP adds a Content-Security-Policy to the admin UI / landing pages.
// These pages keep the account API key in localStorage, so script-src carries
// no 'unsafe-inline': an injected <script> or on*= attribute cannot run and read
// it. The pages' own inline scripts run under a per-response nonce, which the
// handlers stamp onto them with stampNonce. Inline styles stay allowed.
func (p *AssetPipeline) AdminUICSP(next http.Handler) http.Handler {
	return p.uiCSP(next, "'self' blob:")
}
