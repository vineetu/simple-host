package call

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/secrets"
)

// Limits on one call.
const (
	MaxRequestBytes  = 1 << 20
	MaxResponseBytes = 10 << 20
)

// admit is the key's who for the caller Access resolved, through Data's one
// gate: a site's passcode and named viewers always cover its keys, and a
// browser call needs X-SH-CSRF. owner is the owner's credential or the owner
// signed in on the site's own address; signed-in is any active visitor
// session on this site; anyone is everyone the gate lets in.
func admit(ctx context.Context, svc *data.Service, c data.Call, who string) error {
	c.Context = ctx
	c.Resource.SitePasscode = "inherit"
	if err := svc.Gate(c, true); err != nil {
		return err
	}
	if c.Owner || who == "anyone" {
		return nil
	}
	if c.VisitorID == "" {
		return &data.Error{Code: "sign_in_required", Message: "sign in to this site first"}
	}
	if err := svc.VisitorActive(&c); err != nil {
		return err
	}
	if who == "signed-in" || svc.OwnerOnSite(ctx, c) {
		return nil
	}
	return &data.Error{Code: "forbidden", Message: "only the site's owner may use this key"}
}

// target is the request to send: always https://<destination>/<rest>, the
// host set as a field so no path can change it, with the key attached.
// rest is the path after .../call/, as the mux decoded it.
func target(ctx context.Context, r *http.Request, rest, value string, v secrets.Secret) (*http.Request, error) {
	u := &url.URL{Scheme: "https", Host: v.Destination, Path: "/" + rest, RawQuery: r.URL.RawQuery}
	if v.Attach == "query" {
		q := r.URL.Query()
		q.Set(v.AttachName, v.AttachPrefix+value)
		u.RawQuery = q.Encode()
	}
	out, err := http.NewRequestWithContext(ctx, r.Method, u.String(), http.MaxBytesReader(nil, r.Body, MaxRequestBytes))
	if err != nil || out.URL.Hostname() != v.Destination || out.URL.Port() != "" {
		return nil, &data.Error{Code: "invalid_path", Message: "the path is not valid"}
	}
	if out.ContentLength = r.ContentLength; r.ContentLength == 0 {
		out.Body = http.NoBody
	}
	for name, values := range r.Header {
		if sent(name, v) {
			out.Header[name] = values
		}
	}
	if v.Attach == "header" {
		out.Header.Set(v.AttachName, v.AttachPrefix+value)
	}
	return out, nil
}

// sent reports whether the page's header name goes out: the body's type, what
// it accepts, and a service's own x-, anthropic- or openai- headers; never a
// cookie, a credential of this server, a forwarding header or the key's own.
func sent(name string, v secrets.Secret) bool {
	n := strings.ToLower(name)
	if n == strings.ToLower(v.AttachName) {
		return false
	}
	if n == "content-type" || n == "accept" {
		return true
	}
	for _, own := range []string{"x-api-key", "x-sh-", "x-photo-", "x-forwarded-", "x-real-ip", "x-original-", "x-simple-host-", "x-skill-", "x-account-", "x-mcp-", "x-test-"} {
		if strings.HasPrefix(n, own) {
			return false
		}
	}
	return strings.HasPrefix(n, "x-") || strings.HasPrefix(n, "anthropic-") || strings.HasPrefix(n, "openai-")
}

// kept reports whether an answer's header name goes back to the page:
// never a cookie, a hop-by-hop header, the length (the body is capped), a
// CORS header (this server's own decide) or a header nginx acts on.
func kept(name string) bool {
	switch strings.ToLower(name) {
	case "set-cookie", "set-cookie2", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade", "content-length", "alt-svc":
		return false
	}
	n := strings.ToLower(name)
	// x-accel-* would be obeyed by nginx in front of this server
	// (X-Accel-Redirect to an internal location, buffering, rate).
	return !strings.HasPrefix(n, "access-control-") && !strings.HasPrefix(n, "x-accel-")
}
