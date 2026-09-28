package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// Report a page (GET /report, POST /report).
//
// Anyone can report a page hosted here: its address, a reason from a short
// list, optional details and an optional email. The report is emailed to the
// support address through the app's own mail sender (Resend); nothing is
// stored. The form is the on-platform notice the terms point to (intimate
// images, CSAM, phishing and the rest), next to plain email.

const (
	reportMaxURL     = 2048
	reportMaxDetails = 4000
	reportMaxEmail   = 254
	reportMaxBody    = 16 << 10
)

// reportReasons maps each reason the form offers to the words in the email.
var reportReasons = map[string]string{
	"phishing":   "Phishing or scam",
	"malware":    "Malware",
	"csam":       "Child sexual abuse material",
	"ncii":       "Intimate image without consent",
	"copyright":  "Copyright or trademark",
	"harassment": "Harassment or private information",
	"spam":       "Spam",
	"other":      "Other",
}

// reportHandler holds what POST /report needs, so tests can swap each part.
type reportHandler struct {
	mailer replyNoticeSender
	to     string
	// hosted says whether host is served by this service: the platform
	// domain and its subdomains, or a bound custom domain.
	hosted func(ctx context.Context, host string) (bool, error)
	perIP  *rateLimiter
	global *rateLimiter
}

// ReportHandler builds POST /report for this server: mail goes out through
// the site handler's mailer to the support contact.
func (h *SiteHandler) ReportHandler() http.Handler {
	rh := &reportHandler{
		to:     auth.SupportContact,
		perIP:  newRateLimiter(5, 1.0/600),    // 5 reports, then one every 10 minutes
		global: newRateLimiter(60, 60.0/3600), // 60, then 60 an hour, everyone together
		hosted: func(ctx context.Context, host string) (bool, error) {
			if isPlatformHost(host, h.siteDomain) {
				return true, nil
			}
			if h.database == nil {
				return false, nil
			}
			_, err := db.GetSiteByCustomDomain(ctx, h.database, host)
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return err == nil, err
		},
	}
	if m, ok := h.mailer.(replyNoticeSender); ok {
		rh.mailer = m
	}
	rh.perIP.startCleanup(10*time.Minute, time.Hour)
	return rh
}

// isPlatformHost: host is the platform domain or any name under it.
func isPlatformHost(host, siteDomain string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	siteDomain = strings.ToLower(siteDomain)
	if siteDomain == "" {
		return false
	}
	return host == siteDomain || strings.HasSuffix(host, "."+siteDomain)
}

func (rh *reportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	refuse := func(status int, code, msg string) {
		writeJSON(w, status, errorResponse{Error: msg, Code: code})
	}
	if !sameOriginRequest(r) {
		refuse(http.StatusForbidden, "cross_origin", "Send reports from the form at /report.")
		return
	}
	if !rh.perIP.allow(clientIP(r)) || !rh.global.allow("all") {
		w.Header().Set("Retry-After", "600")
		refuse(http.StatusTooManyRequests, "rate_limited", "Too many reports from here. Wait a few minutes, or email "+rh.to+".")
		return
	}
	var body struct {
		URL     string `json:"url"`
		Reason  string `json:"reason"`
		Details string `json:"details"`
		Email   string `json:"email"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, reportMaxBody)).Decode(&body); err != nil {
		refuse(http.StatusBadRequest, "invalid_body", "The report could not be read. Try again.")
		return
	}
	body.URL = strings.TrimSpace(body.URL)
	body.Details = strings.TrimSpace(body.Details)
	body.Email = strings.TrimSpace(body.Email)

	if body.URL == "" || utf8.RuneCountInString(body.URL) > reportMaxURL {
		refuse(http.StatusBadRequest, "invalid_url", "Enter the page's full address, starting with https://.")
		return
	}
	u, err := url.Parse(body.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		refuse(http.StatusBadRequest, "invalid_url", "Enter the page's full address, starting with https://.")
		return
	}
	reason, ok := reportReasons[body.Reason]
	if !ok {
		refuse(http.StatusBadRequest, "invalid_reason", "Choose a reason.")
		return
	}
	if utf8.RuneCountInString(body.Details) > reportMaxDetails {
		refuse(http.StatusBadRequest, "details_too_long", "Keep the details under 4,000 characters.")
		return
	}
	if body.Email != "" {
		a, err := mail.ParseAddress(body.Email)
		if err != nil || a.Address != body.Email || len(body.Email) > reportMaxEmail {
			refuse(http.StatusBadRequest, "invalid_email", "That email address does not look right. Leave it empty if you do not want an answer.")
			return
		}
	}
	host := strings.ToLower(u.Hostname())
	hosted, err := rh.hosted(r.Context(), host)
	if err != nil {
		log.Printf("report: host lookup failed: %v", err)
		refuse(http.StatusServiceUnavailable, "unavailable", "Reports cannot be sent right now. Email "+rh.to+".")
		return
	}
	if !hosted {
		refuse(http.StatusBadRequest, "not_hosted", "That address is not a site on Simple Host. Only pages hosted here can be reported here.")
		return
	}
	if rh.mailer == nil {
		refuse(http.StatusServiceUnavailable, "unavailable", "Reports cannot be sent from this server. Email "+rh.to+".")
		return
	}

	from := body.Email
	if from == "" {
		from = "(not given)"
	}
	details := body.Details
	if details == "" {
		details = "(none)"
	}
	text := "A page was reported through the form at /report.\n\n" +
		"Page: " + body.URL + "\n" +
		"Reason: " + reason + "\n" +
		"Reporter's email: " + from + "\n\n" +
		"Details:\n" + details + "\n"
	subject := "Report: " + reason + " — " + host
	if err := rh.mailer.SendNoticeReplyTo(rh.to, body.Email, subject, text); err != nil {
		log.Printf("report: email failed: %v", err)
		refuse(http.StatusBadGateway, "send_failed", "The report could not be sent. Try again, or email "+rh.to+".")
		return
	}
	log.Printf("report: sent reason=%s host=%s", body.Reason, host)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
