package domains

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

func (h Handler) CheckBoundDomains(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, sitesroute.DomainPassTimeout)
	defer cancel()

	if err := h.svc().PruneDomainCertRequests(ctx); err != nil {
		sites.ReportWorkerFailure(ctx, "prune domain certificate requests", err)
	}
	released, err := h.svc().ReleaseExpiredDomains(ctx, h.Limits.DomainUnprovenMaxAge, h.Limits.DomainUnprovenTTL)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "release expired domains", err)
		return
	}
	for _, info := range released {
		h.svc().ReleaseDomainFiles(info)
		h.Hosts.CancelDomainCert(info.Domain)
		log.Printf("domain: released unproven binding %s from %s", info.Domain, info.Name)
	}

	due, err := h.svc().ListDomainsToCheck(ctx, sitesroute.DomainActiveAge)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "list domains due for checks", err)
		return
	}
	if len(due) == 0 {
		return
	}

	ours := h.Hosts.ServerAddrs(ctx)
	for _, d := range due {
		// A claimed <name>.<SITE_DOMAIN> is ours by construction (wildcard DNS
		// and certificate); there is nothing outside to re-prove.
		if h.Hosts.IsPlatformSubdomain(d.Domain) {
			continue
		}
		h.CheckDomain(ctx, d, ours)
	}
}

// CheckDomain proves one bound custom domain and acts on the result: asks for
// its certificate once its ownership record matches and DNS points here,
// finishes a switch of address once it is verified, and lets a long-failing
// verified domain go. A taken-down site gets no check and no certificate.
func (h Handler) CheckDomain(ctx context.Context, d model.BoundDomain, ours map[string]bool) {
	if d.Suspended {
		h.Hosts.CancelDomainCert(d.Domain)
		return
	}
	var status, reason string
	var pointsHere bool
	// Ownership first: a domain verified before the TXT proof existed keeps
	// working without it while it stays verified; everything else needs it.
	proven, why := true, ""
	if !(d.Verified && d.ProofExempt) {
		proven, why = sitesroute.DomainProof(ctx, d.Domain, d.Token)
	}
	if proven {
		status, reason, pointsHere = h.verifyDomain(ctx, d.Domain, ours)
	} else {
		status, reason = "pending", why
	}
	cert := "pending"
	switch {
	case !proven:
		h.Hosts.CancelDomainCert(d.Domain)
	case status == "active":
		cert = "live"
		// A live domain whose www / bare partner is not set up yet asks
		// again once the partner points here.
		if h.wantPartnerCert(ctx, d, ours) {
			h.requestDomainCert(ctx, d)
		} else {
			h.Hosts.CancelDomainCert(d.Domain)
		}
	case !pointsHere:
		h.Hosts.CancelDomainCert(d.Domain)
	default:
		cert, why = h.DomainCertProgress(ctx, d)
		switch cert {
		case "failed":
			reason = why
		case "issuing":
			reason = "resolves to this server; its certificate is being issued (usually a few minutes)"
		}
	}
	h.ApplyDomainCheck(ctx, d, status, reason, cert)
	if status != "active" && d.PreviousDomain != "" && !h.Hosts.IsPlatformSubdomain(d.PreviousDomain) {
		h.CheckPreviousDomain(ctx, d, ours)
	}
}

// CheckPreviousDomain proves the earlier address a site still serves while its
// new domain is pending. One that keeps failing for domainLapseAfter is let
// go: the site stops serving there and the domain is free for whoever holds it.
func (h Handler) CheckPreviousDomain(ctx context.Context, d model.BoundDomain, ours map[string]bool) {
	status, _, _ := h.verifyDomain(ctx, d.PreviousDomain, ours)
	since, err := h.svc().SetPreviousDomainCheck(ctx, d.SiteID, d.PreviousDomain, status == "active")
	if err != nil {
		sites.ReportWorkerFailure(ctx, "record previous domain check", err)
		return
	}
	if !since.Valid || time.Since(since.Time) < h.Limits.DomainLapseAfter {
		return
	}
	info, ok, err := h.OpsSitesSvc().GetSiteDomainInfo(ctx, d.SiteID)
	if err != nil || !ok {
		return
	}
	lapsed, err := h.svc().LapsePreviousDomain(ctx, d.SiteID, d.PreviousDomain)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "lapse previous domain", err)
		return
	}
	if !lapsed {
		return
	}
	if _, err := h.Disk.UnbindDomainOf(d.PreviousDomain, info.UserID, info.Name); err != nil {
		sites.ReportWorkerFailure(ctx, "unbind previous domain", err)
	}
	h.Hosts.SyncDomainRedirect(ctx, d.SiteID)
	log.Printf("domain %s: earlier address of %s failing since %s; let go", d.PreviousDomain, info.Name, since.Time.UTC().Format(time.RFC3339))
}

func (h Handler) ApplyDomainCheck(ctx context.Context, d model.BoundDomain, status, reason, cert string) {
	check, err := h.svc().SetDomainStatus(ctx, d.SiteID, d.Domain, status, reason, cert)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "record domain check", err)
		return
	}
	if check.UserID != "" {
		sites.ReportWorkerProgress(ctx)
	}
	if status != d.Status {
		log.Printf("domain %s: %s -> %s %s", d.Domain, d.Status, status, reason)
	}
	// Only when the row took it (an empty check means the site no longer has
	// this domain), and against the status read under the row lock, so two
	// overlapping checks notify once.
	if check.UserID != "" && check.PreviousStatus != status {
		h.Notify.EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: d.SiteID, Name: "domain.status_changed", Data: map[string]any{"domain": d.Domain, "status": status, "previous_status": check.PreviousStatus, "reason": notify.Cut(reason, 1024), "cert": cert}})
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
	due := failing >= h.Limits.DomainLapseAfter
	if check.ReleaseAt.Valid {
		due = !time.Now().Before(check.ReleaseAt.Time)
	}
	switch {
	case due:
		if lapsed, err := h.svc().LapseDomain(ctx, d.SiteID, d.Domain); err != nil {
			sites.ReportWorkerFailure(ctx, "lapse domain", err)
		} else if lapsed {
			// Released fully: the link goes, so the issuer drops its server and
			// certificate and nobody inherits a live address.
			if _, err := h.Disk.UnbindDomainOf(d.Domain, check.UserID, check.Name); err != nil {
				sites.ReportWorkerFailure(ctx, "unbind lapsed domain", err)
			}
			h.Hosts.CancelDomainCert(d.Domain)
			h.Hosts.SyncDomainRedirect(ctx, d.SiteID)
			log.Printf("domain %s: failing for %s; released from %s", d.Domain, failing.Round(time.Hour), check.Name)
			h.Notify.EmitSiteEvent(h.Hosts, sites.SiteEvent{SiteID: d.SiteID, Name: "domain.status_changed", Data: map[string]any{"domain": d.Domain, "status": "released", "previous_status": status, "reason": notify.Cut(reason, 1024), "cert": ""}})
		}
	case failing >= h.Limits.DomainLapseWarnAfter && !check.Notified:
		h.EmailDomainFailing(ctx, d, check.Name, reason)
	}
}

// domainVerified runs when a custom domain is proven for the first time (or
// again after a lapse): the site's earlier address is let go (a claimed name
// stays with the site and redirects here) and old links follow the domain.
func (h Handler) domainVerified(ctx context.Context, d model.BoundDomain) {
	prev, err := h.svc().PromoteDomain(ctx, d.SiteID)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "finish domain switch", err)
	}
	if prev != "" {
		if err := h.Disk.UnbindDomain(prev); err != nil {
			sites.ReportWorkerFailure(ctx, "unbind earlier domain", err)
		}
		log.Printf("domain %s: live; earlier address %s let go", d.Domain, prev)
	}
	h.Hosts.SyncDomainRedirect(ctx, d.SiteID)
}

func (h Handler) EmailDomainFailing(ctx context.Context, d model.BoundDomain, siteName, reason string) {
	mailer, ok := h.Mailer.(email.NoticeSender)
	if ok {
		h.Reporting.NotifyDomainFailing(ctx, d, siteName, reason, mailer, h.Ed)
	}
}

// verifyDomain decides the status a bound domain should have. Both halves must
// hold for "active": it resolves here AND it serves the site over HTTPS. The
// fetch is the proof — DNS pointing at us has never meant the site is up — and
// DNS is read to explain a failed fetch and to know when to ask for the
// domain's certificate (pointsHere).
func (h Handler) verifyDomain(ctx context.Context, domain string, ours map[string]bool) (status, reason string, pointsHere bool) {
	ips, err := sites.LookupHost(ctx, domain)
	if err != nil {
		return "pending", "domain does not resolve yet", false
	}
	here := ""
	for _, ip := range ips {
		if ours[ip] && notify.PublicIP(ip) {
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
	resp, err := sitesroute.DomainProbeVia(here).Do(req)
	if err != nil {
		return "pending", "resolves to this server but HTTPS is not answering yet (certificate not issued)", true
	}
	resp.Body.Close()

	if resp.StatusCode/100 == 2 {
		return "active", "", true
	}
	return "error", fmt.Sprintf("HTTPS returned %d", resp.StatusCode), true
}

// checkDomainNow is the owner's "Check again": the background pass for this one site, answered like GET /domain.
func (h Handler) checkDomainNow(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	site, err := h.OpsSitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			sitesroute.WriteSiteNotFound(w, siteName)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if web.RefuseSuspendedSite(w, site) {
		return
	}
	// Each check reaches out to the domain; a few per account at a time.
	if h.DomainCheckUserLimiter != nil && !h.DomainCheckUserLimiter.AllowHTTP(w, user.ID) {
		httpx.TooManyRequests(w)
		return
	}
	info, ok, err := h.OpsSitesSvc().GetSiteDomainInfo(r.Context(), site.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "no_domain", "this site has no domain connected")
		return
	}
	if !h.Hosts.IsPlatformSubdomain(info.Domain) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*sitesroute.DomainProbeTimeout)
		defer cancel()
		// The same check, certificate hand-off and switch of address as the
		// background pass.
		h.CheckDomain(ctx, model.BoundDomainOf(info), h.Hosts.ServerAddrs(ctx))
	}
	h.getDomain(w, r)
}
