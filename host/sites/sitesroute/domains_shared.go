package sitesroute

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

const (
	DomainActiveAge   = time.Hour
	DomainPassTimeout = 2 * time.Minute
)

// DomainProbeTimeout bounds one HTTPS probe (a variable so tests can shorten it).
var DomainProbeTimeout = 10 * time.Second

// DomainProof reports whether the DNS TXT record _simple-host.<domain> holds
// the site's token: the proof that whoever connected the domain controls it.
// Only this counts; something on this server answering the domain proves
// nothing about who owns it.
func DomainProof(ctx context.Context, domain, token string) (bool, string) {
	why := "add the ownership record at your domain's registrar: TXT " + DomainProofHost(domain) + " with the value " + token
	if token == "" {
		return false, "the ownership record could not be checked yet"
	}
	recs, err := sites.LookupTXT(ctx, DomainProofHost(domain))
	if err != nil {
		return false, why
	}
	for _, r := range recs {
		if strings.Trim(strings.TrimSpace(r), `"`) == token {
			return true, ""
		}
	}
	return false, "the TXT record " + DomainProofHost(domain) + " does not hold this site's value (" + token + ")"
}

// DomainProbeVia is the HTTPS client for one check: it connects only to ip
// (an address of this server the domain resolved to, so a second DNS answer
// cannot send the request anywhere else) and never follows a redirect.
func DomainProbeVia(ip string) *http.Client {
	dialer := &net.Dialer{Timeout: DomainProbeTimeout}
	return &http.Client{
		Timeout: DomainProbeTimeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			},
			TLSHandshakeTimeout: DomainProbeTimeout,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NormalizeDomain trims, lowercases, strips a leading scheme and any path, and
// strips a trailing dot. Rejects empty, non-ASCII, missing-dot, path/space
// residues, our own platform hosts, and malformed labels. Total length ≤253.
func (h *Handler) NormalizeDomain(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("domain is required")
	}

	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	s = strings.ToLower(s)

	if s == "" {
		return "", errors.New("domain is required")
	}
	if err := sites.DomainShape(s); err != nil {
		return "", err
	}

	// Reject hijacking our own zone: exact match or subdomain of siteDomain /
	// contentHost (covers cname.<siteDomain> when CNAME_TARGET uses the default).
	if IsOwnHost(s, h.ContentHost) {
		return "", errors.New("cannot bind a platform host as a custom domain")
	}
	for _, b := range h.Hosts.Addressing().KnownBases() {
		if IsOwnHost(s, b) {
			return "", errors.New("cannot bind a platform host as a custom domain")
		}
	}
	// The platform's other zones (the public site, event domains such as
	// simple-hack.app).
	for _, z := range h.Hosts.PlatformZones() {
		if IsOwnHost(s, z) {
			return "", errors.New("cannot bind a platform host as a custom domain")
		}
	}

	return s, nil
}

// DomainCandidate lowercases raw and strips a scheme, path, port and trailing
// dot — the same clean-up normalizeDomain starts with — without validating.
func DomainCandidate(raw string) string {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

func IsOwnHost(host, base string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	base = strings.ToLower(strings.TrimSpace(base))
	if host == "" || base == "" {
		return false
	}
	if host == base {
		return true
	}
	return strings.HasSuffix(host, "."+base)
}

func IsFamilyRequest(raw string) bool {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	return strings.HasPrefix(s, "*.")
}

// MarkRestrictedSite sets noindex before an address redirect can return; the
// file-serving gates set their own headers, including disk-only gates.
func MarkRestrictedSite(w http.ResponseWriter, site model.Site) {
	if !site.OpenToAnyone() {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
}
