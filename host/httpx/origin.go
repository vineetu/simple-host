package httpx

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func HostName(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

// Origin is the scheme://host the browser says the request came from:
// Origin, else the Referer's origin, else this request's own origin when the
// browser marks it Sec-Fetch-Site: same-origin. A page with
// <meta name="referrer" content="no-referrer"> sends no Referer and no Origin
// on its own GETs, and Origin: null on its writes; Fetch Metadata still names
// it, and a page script cannot set a Sec- header. Empty when none is usable.
func Origin(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		return o
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" && r.Host != "" {
		scheme := "http"
		if IsHTTPS(r) {
			scheme = "https"
		}
		return scheme + "://" + r.Host
	}
	return ""
}

// SameOrigin: the request was made by a page on this very origin. A
// browser cannot forge Origin/Referer or Sec-Fetch-Site; a script outside a
// browser can, but then it has no visitor cookie to go with them.
func SameOrigin(r *http.Request) bool {
	if CrossSite(r) {
		return false
	}
	o := Origin(r)
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	want := "http"
	if IsHTTPS(r) {
		want = "https"
	}
	return strings.EqualFold(u.Scheme, want) && strings.EqualFold(u.Host, r.Host)
}

// CrossSite: the browser says a page on another origin made the request.
func CrossSite(r *http.Request) bool {
	s := r.Header.Get("Sec-Fetch-Site")
	return s != "" && s != "same-origin"
}
