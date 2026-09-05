package handler

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

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
		status, reason := h.verifyDomain(ctx, d.Domain, ours)
		if err := db.SetDomainStatus(ctx, h.database, d.SiteID, status, reason); err != nil {
			log.Printf("domain check %s: %v", d.Domain, err)
			continue
		}
		if status != d.Status {
			log.Printf("domain %s: %s -> %s %s", d.Domain, d.Status, status, reason)
		}
	}
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
// DNS is read only to explain a failed fetch, because "not pointed here yet" and
// "pointed here but no certificate" need different actions from the operator
// (certificates are issued by hand, per domain).
func (h *SiteHandler) verifyDomain(ctx context.Context, domain string, ours map[string]bool) (status, reason string) {
	ips, err := net.DefaultResolver.LookupHost(ctx, domain)
	if err != nil {
		return "pending", "domain does not resolve yet"
	}
	pointsHere := false
	for _, ip := range ips {
		if ours[ip] {
			pointsHere = true
			break
		}
	}
	if !pointsHere {
		return "pending", "resolves to " + strings.Join(ips, ", ") + ", not to this server"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
	if err != nil {
		return "pending", err.Error()
	}
	resp, err := domainProbe.Do(req)
	if err != nil {
		return "pending", "resolves to this server but HTTPS is not answering yet (certificate not issued)"
	}
	resp.Body.Close()

	if resp.StatusCode/100 == 2 {
		return "active", ""
	}
	return "error", fmt.Sprintf("HTTPS returned %d", resp.StatusCode)
}
