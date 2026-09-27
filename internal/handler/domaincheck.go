package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

const (
	// domainActiveAge is how long an "active" verdict stands before it must be
	// proved again.
	domainActiveAge = time.Hour
	// domainPassTimeout bounds one whole pass over the due domains.
	domainPassTimeout = 2 * time.Minute
)

// lookupTXT and lookupHost read DNS (variables so tests can stand in).
// domainProbeTimeout bounds one HTTPS probe (a variable so tests can shorten it).
var domainProbeTimeout = 10 * time.Second

var (
	lookupTXT  = net.DefaultResolver.LookupTXT
	lookupHost = net.DefaultResolver.LookupHost
)

// domainProofHost is where a custom domain's ownership record lives.
func domainProofHost(domain string) string { return "_simple-host." + domain }

// domainProof reports whether the DNS TXT record _simple-host.<domain> holds
// the site's token: the proof that whoever connected the domain controls it.
// Only this counts; something on this server answering the domain proves
// nothing about who owns it.
func domainProof(ctx context.Context, domain, token string) (bool, string) {
	why := "add the ownership record at your domain's registrar: TXT " + domainProofHost(domain) + " with the value " + token
	if token == "" {
		return false, "the ownership record could not be checked yet"
	}
	recs, err := lookupTXT(ctx, domainProofHost(domain))
	if err != nil {
		return false, why
	}
	for _, r := range recs {
		if strings.Trim(strings.TrimSpace(r), `"`) == token {
			return true, ""
		}
	}
	return false, "the TXT record " + domainProofHost(domain) + " does not hold this site's value (" + token + ")"
}

// domainProbeVia is the HTTPS client for one check: it connects only to ip
// (an address of this server the domain resolved to, so a second DNS answer
// cannot send the request anywhere else) and never follows a redirect.
func domainProbeVia(ip string) *http.Client {
	dialer := &net.Dialer{Timeout: domainProbeTimeout}
	return &http.Client{
		Timeout: domainProbeTimeout,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				_, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
			},
			TLSHandshakeTimeout: domainProbeTimeout,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// isPublicIP: a globally routable unicast address (not loopback, private,
// link-local, carrier-grade NAT, unspecified or multicast).
func isPublicIP(s string) bool {
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range nonPublicPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

var nonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
}

// startDomainChecks re-verifies bound custom domains in the background, so
// domain_status reflects what the domain actually does rather than what was
// hoped for at bind time.
func (h *SiteHandler) startDomainChecks(every time.Duration) {
	go func() {
		time.Sleep(30 * time.Second)
		for {
			h.checkBoundDomains()
			time.Sleep(every)
		}
	}()
}

func (h *SiteHandler) checkBoundDomains() {
	ctx, cancel := context.WithTimeout(context.Background(), domainPassTimeout)
	defer cancel()

	if err := db.PruneDomainCertRequests(ctx, h.database); err != nil {
		log.Printf("domain check: prune certificate requests: %v", err)
	}
	released, err := db.ReleaseExpiredDomains(ctx, h.database)
	if err != nil {
		log.Printf("domain check: expiry failed: %v", err)
		return
	}
	for _, info := range released {
		h.releaseDomainFiles(info)
		h.cancelDomainCert(info.Domain)
		log.Printf("domain: released unproven binding %s from %s", info.Domain, info.Name)
	}

	due, err := db.ListDomainsToCheck(ctx, h.database, domainActiveAge)
	if err != nil {
		log.Printf("domain check: list failed: %v", err)
		return
	}
	if len(due) == 0 {
		return
	}

	ours := h.serverAddrs(ctx)
	for _, d := range due {
		// A claimed <name>.<SITE_DOMAIN> is ours by construction (wildcard DNS
		// and certificate); there is nothing outside to re-prove.
		if h.isPlatformSubdomainHost(d.Domain) {
			continue
		}
		h.checkDomain(ctx, d, ours)
	}
}

// checkDomain proves one bound custom domain and acts on the result: asks for
// its certificate once its ownership record matches and DNS points here,
// finishes a switch of address once it is verified, and lets a long-failing
// verified domain go. A taken-down site gets no check and no certificate.
func (h *SiteHandler) checkDomain(ctx context.Context, d db.BoundDomain, ours map[string]bool) {
	if d.Suspended {
		h.cancelDomainCert(d.Domain)
		return
	}
	var status, reason string
	var pointsHere bool
	// Ownership first: a domain verified before the TXT proof existed keeps
	// working without it while it stays verified; everything else needs it.
	proven, why := true, ""
	if !(d.Verified && d.ProofExempt) {
		proven, why = domainProof(ctx, d.Domain, d.Token)
	}
	if proven {
		status, reason, pointsHere = h.verifyDomain(ctx, d.Domain, ours)
	} else {
		status, reason = "pending", why
	}
	cert := "pending"
	switch {
	case !proven:
		h.cancelDomainCert(d.Domain)
	case status == "active":
		cert = "live"
		// A live domain whose www / bare partner is not set up yet asks
		// again once the partner points here.
		if h.wantPartnerCert(ctx, d, ours) {
			h.requestDomainCert(ctx, d)
		} else {
			h.cancelDomainCert(d.Domain)
		}
	case !pointsHere:
		h.cancelDomainCert(d.Domain)
	default:
		cert, why = h.domainCertProgress(ctx, d)
		switch cert {
		case "failed":
			reason = why
		case "issuing":
			reason = "resolves to this server; its certificate is being issued (usually a few minutes)"
		}
	}
	h.applyDomainCheck(ctx, d, status, reason, cert)
	if status != "active" && d.PreviousDomain != "" && !h.isPlatformSubdomainHost(d.PreviousDomain) {
		h.checkPreviousDomain(ctx, d, ours)
	}
}

// checkPreviousDomain proves the earlier address a site still serves while its
// new domain is pending. One that keeps failing for domainLapseAfter is let
// go: the site stops serving there and the domain is free for whoever holds it.
func (h *SiteHandler) checkPreviousDomain(ctx context.Context, d db.BoundDomain, ours map[string]bool) {
	status, _, _ := h.verifyDomain(ctx, d.PreviousDomain, ours)
	since, err := db.SetPreviousDomainCheck(ctx, h.database, d.SiteID, d.PreviousDomain, status == "active")
	if err != nil {
		log.Printf("domain check %s (earlier address): %v", d.PreviousDomain, err)
		return
	}
	if !since.Valid || time.Since(since.Time) < domainLapseAfter() {
		return
	}
	info, ok, err := db.GetSiteDomainInfo(ctx, h.database, d.SiteID)
	if err != nil || !ok {
		return
	}
	lapsed, err := db.LapsePreviousDomain(ctx, h.database, d.SiteID, d.PreviousDomain)
	if err != nil {
		log.Printf("domain %s: lapse earlier address: %v", d.PreviousDomain, err)
		return
	}
	if !lapsed {
		return
	}
	if _, err := h.disk.UnbindDomainOf(d.PreviousDomain, info.UserID, info.Name); err != nil {
		log.Printf("domain: unbind earlier %s: %v", d.PreviousDomain, err)
	}
	h.syncDomainRedirect(ctx, d.SiteID)
	log.Printf("domain %s: earlier address of %s failing since %s; let go", d.PreviousDomain, info.Name, since.Time.UTC().Format(time.RFC3339))
}

// applyDomainCheck records one check's verdict and acts on it.
func (h *SiteHandler) applyDomainCheck(ctx context.Context, d db.BoundDomain, status, reason, cert string) {
	check, err := db.SetDomainStatus(ctx, h.database, d.SiteID, d.Domain, status, reason, cert)
	if err != nil {
		log.Printf("domain check %s: %v", d.Domain, err)
		return
	}
	if status != d.Status {
		log.Printf("domain %s: %s -> %s %s", d.Domain, d.Status, status, reason)
	}
	if status == "active" {
		if !d.Verified || d.PreviousDomain != "" {
			h.domainVerified(ctx, d)
		}
		return
	}
	if !check.FailingSince.Valid {
		return
	}
	failing := time.Since(check.FailingSince.Time)
	// Once the owner was emailed, the date that email gave decides (a later
	// DOMAIN_LAPSE_HOURS change applies to new warnings only).
	due := failing >= domainLapseAfter()
	if check.ReleaseAt.Valid {
		due = !time.Now().Before(check.ReleaseAt.Time)
	}
	switch {
	case due:
		if lapsed, err := db.LapseDomain(ctx, h.database, d.SiteID, d.Domain); err != nil {
			log.Printf("domain %s: lapse: %v", d.Domain, err)
		} else if lapsed {
			// Released fully: the link goes, so the issuer drops its server and
			// certificate and nobody inherits a live address.
			if _, err := h.disk.UnbindDomainOf(d.Domain, check.UserID, check.Name); err != nil {
				log.Printf("domain: unbind lapsed %s: %v", d.Domain, err)
			}
			h.cancelDomainCert(d.Domain)
			h.syncDomainRedirect(ctx, d.SiteID)
			log.Printf("domain %s: failing for %s; released from %s", d.Domain, failing.Round(time.Hour), check.Name)
		}
	case failing >= domainLapseWarnAfter() && !check.Notified:
		h.emailDomainFailing(ctx, d, check.Name, reason)
	}
}

// domainVerified runs when a custom domain is proven for the first time (or
// again after a lapse): the site's earlier address is let go (a claimed name
// stays with the site and redirects here) and old links follow the domain.
func (h *SiteHandler) domainVerified(ctx context.Context, d db.BoundDomain) {
	prev, err := db.PromoteDomain(ctx, h.database, d.SiteID)
	if err != nil {
		log.Printf("domain %s: switch from earlier address: %v", d.Domain, err)
	}
	if prev != "" {
		if err := h.disk.UnbindDomain(prev); err != nil {
			log.Printf("domain: unbind earlier %s: %v", prev, err)
		}
		log.Printf("domain %s: live; earlier address %s let go", d.Domain, prev)
	}
	h.syncDomainRedirect(ctx, d.SiteID)
}

// noticeSender is the part of the mailer that sends a plain notice.
type noticeSender interface {
	SendNotice(toEmail, subject, text string) error
}

// emailDomainFailing tells the owner their domain has stopped working and
// what happens next. Marked as sent only when it was.
func (h *SiteHandler) emailDomainFailing(ctx context.Context, d db.BoundDomain, siteName, reason string) {
	mailer, ok := h.mailer.(noticeSender)
	if !ok {
		return
	}
	to, err := db.GetSiteOwnerEmail(ctx, h.database, d.SiteID)
	if err != nil || !strings.Contains(to, "@") {
		return
	}
	if reason == "" {
		reason = "it does not answer over HTTPS"
	}
	subject := "Your domain " + d.Domain + " has stopped working"
	releaseIn := domainLapseAfter() - domainLapseWarnAfter()
	releaseAt := time.Now().Add(releaseIn)
	text := fmt.Sprintf(`Your site %s is connected to https://%s/, and that address has failed every check for %s.

What the last check saw: %s

If you moved the domain or let it lapse on purpose, there is nothing to do. Otherwise, check the domain's DNS records at your registrar (the address record, and the TXT record _simple-host.<domain>).

If it is still failing %s from now, %s is disconnected from the site: the site serves at its own Simple Host address again. Connecting the domain again (by you or whoever holds it then) needs its DNS ownership record (TXT _simple-host.<domain>) once more.

Simple Host
`, siteName, d.Domain, config.Span(domainLapseWarnAfter()), reason, config.Span(releaseIn), d.Domain)
	if err := mailer.SendNotice(to, subject, text); err != nil {
		log.Printf("domain %s: failing notice: %v", d.Domain, err)
		return
	}
	if err := db.MarkDomainLapseNotified(ctx, h.database, d.SiteID, d.Domain, releaseAt); err != nil {
		log.Printf("domain %s: mark notified: %v", d.Domain, err)
	}
	log.Printf("domain %s: owner emailed about failing checks", d.Domain)
}

// serverAddrs is the set of addresses that count as "this server": the A-record
// value handed out for apex domains, plus whatever the CNAME target resolves to,
// since every subdomain is pointed there. Both come from dnsRecordFor, so a
// domain following our own instructions always lands in this set.
func (h *SiteHandler) serverAddrs(ctx context.Context) map[string]bool {
	ours := map[string]bool{}
	if h.customDomainIP != "" {
		ours[h.customDomainIP] = true
	}
	if ips, err := lookupHost(ctx, h.cnameTarget); err == nil {
		for _, ip := range ips {
			ours[ip] = true
		}
	}
	return ours
}

// verifyDomain decides the status a bound domain should have. Both halves must
// hold for "active": it resolves here AND it serves the site over HTTPS. The
// fetch is the proof — DNS pointing at us has never meant the site is up — and
// DNS is read to explain a failed fetch and to know when to ask for the
// domain's certificate (pointsHere).
func (h *SiteHandler) verifyDomain(ctx context.Context, domain string, ours map[string]bool) (status, reason string, pointsHere bool) {
	ips, err := lookupHost(ctx, domain)
	if err != nil {
		return "pending", "domain does not resolve yet", false
	}
	here := ""
	for _, ip := range ips {
		if ours[ip] && isPublicIP(ip) {
			here = ip
			break
		}
	}
	if here == "" {
		return "pending", "resolves to " + strings.Join(ips, ", ") + ", not to this server", false
	}
	pointsHere = true

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
	if err != nil {
		return "pending", err.Error(), true
	}
	resp, err := domainProbeVia(here).Do(req)
	if err != nil {
		return "pending", "resolves to this server but HTTPS is not answering yet (certificate not issued)", true
	}
	resp.Body.Close()

	if resp.StatusCode/100 == 2 {
		return "active", "", true
	}
	return "error", fmt.Sprintf("HTTPS returned %d", resp.StatusCode), true
}

// checkDomainNow is POST /v1/sites/{sitename}/domain/check: the owner's
// "Check again". It proves this one site's domain now, the same way the
// background pass does, and answers like GET /domain.
func (h *SiteHandler) checkDomainNow(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}
	// Each check reaches out to the domain; a few per account at a time.
	if h.domainCheckUserLimiter != nil && !h.domainCheckUserLimiter.allow(user.ID) {
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "checked a moment ago; try again in half a minute (domains are also checked every few minutes on their own)", Code: "rate_limited"})
		return
	}
	info, ok, err := db.GetSiteDomainInfo(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "this site has no domain connected", "code": "no_domain"})
		return
	}
	if !h.isPlatformSubdomainHost(info.Domain) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*domainProbeTimeout)
		defer cancel()
		// The same check, certificate hand-off and switch of address as the
		// background pass.
		h.checkDomain(ctx, db.BoundDomainOf(info), h.serverAddrs(ctx))
	}
	h.getDomain(w, r)
}
