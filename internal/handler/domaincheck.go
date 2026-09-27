package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

const (
	domainCheckInterval = 2 * time.Minute
	// domainActiveAge is how long an "active" verdict stands before it must be
	// proved again.
	domainActiveAge    = time.Hour
	domainProbeTimeout = 10 * time.Second
	// domainPassTimeout bounds one whole pass over the due domains.
	domainPassTimeout = 2 * time.Minute
	// A verified domain that keeps failing its checks: its owner is emailed
	// after domainLapseWarnAfter, and after domainLapseAfter it stops being
	// the site's home (its verification is cleared).
	domainLapseWarnAfter = 24 * time.Hour
	domainLapseAfter     = 72 * time.Hour
)

var domainProbe = &http.Client{Timeout: domainProbeTimeout}

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
// its certificate once DNS points here, finishes a switch of address once it
// is verified, and lets a long-failing verified domain go.
func (h *SiteHandler) checkDomain(ctx context.Context, d db.BoundDomain, ours map[string]bool) {
	status, reason, pointsHere := h.verifyDomain(ctx, d.Domain, ours)
	cert := "pending"
	switch {
	case status == "active":
		cert = "live"
		h.cancelDomainCert(d.Domain)
	case !pointsHere:
		h.cancelDomainCert(d.Domain)
	default:
		var why string
		cert, why = h.domainCertProgress(d.Domain)
		switch cert {
		case "failed":
			reason = why
		case "issuing":
			reason = "resolves to this server; its certificate is being issued (usually a few minutes)"
		}
	}
	h.applyDomainCheck(ctx, d, status, reason, cert)
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
	switch {
	case failing >= domainLapseAfter:
		if lapsed, err := db.LapseDomain(ctx, h.database, d.SiteID, d.Domain); err != nil {
			log.Printf("domain %s: lapse: %v", d.Domain, err)
		} else if lapsed {
			h.syncDomainRedirect(ctx, d.SiteID)
			log.Printf("domain %s: failing for %s; no longer the home of %s", d.Domain, failing.Round(time.Hour), check.Name)
		}
	case failing >= domainLapseWarnAfter && !check.Notified:
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
	text := fmt.Sprintf(`Your site %s is connected to https://%s/, and that address has failed every check for the last day.

What the last check saw: %s

If you moved the domain or let it lapse on purpose, there is nothing to do. Otherwise, check the DNS record at your domain registrar.

If it is still failing two days from now, %s stops being the site's address: the site serves at its own Simple Host address again, and the domain can be connected afresh by whoever holds it.

Simple Host
`, siteName, d.Domain, reason, d.Domain)
	if err := mailer.SendNotice(to, subject, text); err != nil {
		log.Printf("domain %s: failing notice: %v", d.Domain, err)
		return
	}
	if err := db.MarkDomainLapseNotified(ctx, h.database, d.SiteID, d.Domain); err != nil {
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
	if ips, err := net.DefaultResolver.LookupHost(ctx, h.cnameTarget); err == nil {
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
	ips, err := net.DefaultResolver.LookupHost(ctx, domain)
	if err != nil {
		return "pending", "domain does not resolve yet", false
	}
	for _, ip := range ips {
		if ours[ip] {
			pointsHere = true
			break
		}
	}
	if !pointsHere {
		return "pending", "resolves to " + strings.Join(ips, ", ") + ", not to this server", false
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
	if err != nil {
		return "pending", err.Error(), true
	}
	resp, err := domainProbe.Do(req)
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
		h.checkDomain(ctx, db.BoundDomain{SiteID: site.ID, Domain: info.Domain, Status: info.Status,
			Verified: info.VerifiedAt.Valid, PreviousDomain: info.PreviousDomain}, h.serverAddrs(ctx))
	}
	h.getDomain(w, r)
}
