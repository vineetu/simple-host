package siteshttp

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

type siteSummaryFields struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Headings    string `json:"headings,omitempty"`
	Text        string `json:"text,omitempty"`
}

type SiteResponse struct {
	Pinned        bool      `json:"pinned"`
	Order         int       `json:"order"`
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Name          string    `json:"name"`
	ActiveVersion int       `json:"active_version"`
	SiteURL       string    `json:"site_url"`
	LiveURL       string    `json:"live_url"` // sites.LiveURL: the address to hand out
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CustomDomain  string    `json:"custom_domain,omitempty"`
	DomainStatus  string    `json:"domain_status,omitempty"`
	// Only in the site list: compact content from the latest uploaded index.
	Summary *siteSummaryFields `json:"summary,omitempty"`
	// Only in the site list, on every item when the analytics log is read:
	// views by people over the rolling
	// last 7 and 30 days, the owner's own visits excluded (operations.SiteViews).
	Views *operations.SiteViews `json:"views,omitempty"`
	// Only in the site list: what is wrong with a domain that is not active
	// yet, the DNS record it needs, and when an unproven binding lapses.
	DomainLastError string     `json:"domain_last_error,omitempty"`
	DomainDNS       *dnsRecord `json:"domain_dns,omitempty"`
	// DomainDNSTXT is the ownership record the domain also needs (TXT
	// _simple-host.<domain> = the site's token).
	DomainDNSTXT    *dnsRecord `json:"domain_dns_txt,omitempty"`
	DomainExpiresAt *time.Time `json:"domain_expires_at,omitempty"`
	// DomainCertStatus is the pending domain's certificate (pending, issuing,
	// live, failed) and PreviousDomain the earlier address the site keeps
	// answering at until the new domain is live.
	DomainCertStatus string `json:"domain_certificate_status,omitempty"`
	PreviousDomain   string `json:"previous_domain,omitempty"`
	// DomainPartner is the custom domain's www / bare partner, a
	// redirect-only host (see GET /domain: partner_domain and friends).
	DomainPartner       string     `json:"domain_partner,omitempty"`
	DomainPartnerDNS    *dnsRecord `json:"domain_partner_dns,omitempty"`
	DomainPartnerStatus string     `json:"domain_partner_status,omitempty"`
	DomainPartnerNote   string     `json:"domain_partner_note,omitempty"`
	// DeployedAt is when the newest version went live (site list only).
	DeployedAt    *time.Time `json:"deployed_at,omitempty"`
	Visibility    string     `json:"visibility,omitempty"`
	OwnerUsername string     `json:"owner_username,omitempty"`
	// Owner and AccessRole: on GET /v1/sites?owner=<team>, the team's
	// handle and member (the caller holds these sites as a member). Absent
	// from a listing of your own sites, which is unchanged.
	Owner      string `json:"owner,omitempty"`
	AccessRole string `json:"access_role,omitempty"`
	Note       string `json:"note,omitempty"`
	// Suspended: the operator has taken the site down (by itself or with its
	// owner's account). It keeps everything and refuses changes until restored.
	Suspended       bool   `json:"suspended,omitempty"`
	SuspendedReason string `json:"suspended_reason,omitempty"`
	// Offline: its owner has taken it offline (every address shows "This
	// site is offline", visitor saves are refused; nothing is deleted).
	Offline bool `json:"offline,omitempty"`
	// PasscodeProtected: the site asks visitors for a passcode on every
	// address (passcode.go). The passcode itself is only in GET .../lock.
	PasscodeProtected bool `json:"passcode_protected,omitempty"`
	// Access: who can open the site, "anyone" or "specific" (named viewers; viewers.go).
	Access       string `json:"access,omitempty"`
	OpenToAnyone bool   `json:"open_to_anyone"` // model.Site.OpenToAnyone
	// Only on a deploy with publish=false: the version it stored (not live)
	// and an hour-long, owner-only preview link for it (preview.go).
	UnpublishedVersion int        `json:"unpublished_version,omitempty"`
	PreviewURL         string     `json:"preview_url,omitempty"`
	PreviewExpiresAt   *time.Time `json:"preview_expires_at,omitempty"`
	// AddressState: present while the site is handed out at its interim
	// address (<handle>.<SITE_DOMAIN>/<site>/) because its owner's
	// certificate is not ready yet — waiting or failing, with a rough time.
	AddressState *sites.AddressState `json:"address_state,omitempty"`
	// Keep: the owner marked the site to stay up for good; it is never
	// removed as idle (site list only).
	Keep bool `json:"keep,omitempty"`
	// KeepVersions: how many deploys of this site are kept, when the owner
	// set it (PUT .../keep-versions); absent = the instance setting.
	KeepVersions int   `json:"keep_versions,omitempty"`
	FileBytes    int64 `json:"file_bytes"`
	// FamilyAddress is the site's address under its account's most specific
	// live address family (the one handed out: its main address unless it
	// has a domain of its own); FamilyAddresses every family address it
	// answers at. site_url stays the site's own Simple Host address.
	FamilyAddress   string   `json:"family_address,omitempty"`
	FamilyAddresses []string `json:"family_addresses,omitempty"`
}

type versionResponse struct {
	VersionNumber int       `json:"version_number"`
	Status        string    `json:"status"`
	Author        string    `json:"author"` // who made it (?author= on the deploy), "" when unset
	CreatedAt     time.Time `json:"created_at"`
	IsActive      bool      `json:"is_active"`
}

type siteResponseState struct {
	siteURL         string
	familyAddresses []string
	familyAddress   string
	addressState    *sites.AddressState
	partner         *partnerInfo
	fileBytes       int64
	previewURL      string
	previewExpires  time.Time
	previewOK       bool
}

func (h *SiteHandler) captureSiteResponseState(site model.Site, version int, preview, footprint bool) siteResponseState {
	state := siteResponseState{siteURL: h.Hosts.Addressing().SiteURLFor(site)}
	for _, a := range h.Hosts.SiteFamilyAddrs(site.UserID, site.Name) {
		state.familyAddresses = append(state.familyAddresses, "https://"+a.Host+"/")
	}
	if host, ok := h.Hosts.SiteFamilyAddress(site.UserID, site.Name); ok {
		state.familyAddress = "https://" + host + "/"
	}
	if !(site.CustomDomain.Valid && site.DomainVerifiedAt.Valid) {
		state.addressState = h.Hosts.SiteAddressStateFor(site.OwnerHandle, site.Name)
	}
	if site.CustomDomain.Valid && site.CustomDomain.String != "" {
		state.partner = h.PartnerInfoFor(site.CustomDomain.String, site.ID)
	}
	if footprint {
		if f, err := h.Disk.SiteFootprint(site.UserID, site.Name); err == nil {
			state.fileBytes = f.Total()
		}
	}
	if preview {
		state.previewURL, state.previewExpires, state.previewOK = h.previewLink(site, version)
	}
	return state
}

func (h *SiteHandler) siteResponseFromState(user *model.User, site model.Site, state siteResponseState) SiteResponse {
	resp := h.toSiteResponseState(site, "", state)
	resp.FileBytes = state.fileBytes
	return h.siteResponseForCaller(user, resp)
}

func (h *SiteHandler) siteResponseForCaller(user *model.User, resp SiteResponse) SiteResponse {
	return resp
}

func (h *SiteHandler) ToSiteResponse(site model.Site, note string) SiteResponse {
	return h.toSiteResponseState(site, note, h.captureSiteResponseState(site, 0, false, false))
}

func (h *SiteHandler) toSiteResponseState(site model.Site, note string, state siteResponseState) SiteResponse {
	visibility := site.Visibility
	if visibility == "" {
		visibility = "unlisted" // never guess "public"
	}
	resp := SiteResponse{
		ID:                site.ID,
		UserID:            site.UserID,
		Name:              site.Name,
		ActiveVersion:     site.ActiveVersion,
		SiteURL:           state.siteURL,
		LiveURL:           sites.LiveURL(site, state.familyAddress, state.siteURL),
		CreatedAt:         site.CreatedAt,
		UpdatedAt:         site.UpdatedAt,
		CustomDomain:      site.CustomDomain.String,
		DomainStatus:      site.DomainStatus.String,
		Visibility:        visibility,
		OwnerUsername:     site.OwnerUsername,
		Note:              note,
		Suspended:         site.Suspended(),
		Offline:           site.Offline,
		SuspendedReason:   site.SuspendedReason(),
		PasscodeProtected: site.Passcode,
		Access:            web.SiteAccessOf(site),
		OpenToAnyone:      site.OpenToAnyone(),
		KeepVersions:      site.KeepVersions,
	}
	if site.SummaryTitle != "" || site.SummaryDescription != "" || site.SummaryHeadings != "" || site.SummaryText != "" {
		resp.Summary = &siteSummaryFields{Title: site.SummaryTitle, Description: site.SummaryDescription, Headings: site.SummaryHeadings, Text: site.SummaryText}
	}
	if site.LastDeployedAt.Valid {
		t := site.LastDeployedAt.Time
		resp.DeployedAt = &t
	}
	resp.FamilyAddresses = state.familyAddresses
	resp.FamilyAddress = state.familyAddress
	if !(site.CustomDomain.Valid && site.DomainVerifiedAt.Valid) {
		resp.AddressState = state.addressState
	}
	if site.CustomDomain.Valid && site.CustomDomain.String != "" {
		if p := state.partner; p != nil {
			resp.DomainPartner, resp.DomainPartnerDNS, resp.DomainPartnerStatus, resp.DomainPartnerNote = p.Domain, p.DNS, p.Status, p.Note
		}
	}
	if site.CustomDomain.Valid && site.CustomDomain.String != "" && site.DomainStatus.String != "active" {
		resp.DomainLastError = site.DomainLastError.String
		if !h.Hosts.IsPlatformSubdomain(site.CustomDomain.String) {
			rec := h.dnsRecordFor(site.CustomDomain.String)
			resp.DomainDNS = &rec
			resp.DomainDNSTXT = proofRecordFor(site.CustomDomain.String, site.DomainToken)
		}
		resp.DomainCertStatus = site.DomainCertStatus
		resp.PreviousDomain = site.PreviousDomain
		// A binding whose DNS already points here (certificate past
		// "pending") does not lapse while it waits.
		if site.DomainBoundAt.Valid && !site.DomainVerifiedAt.Valid && (site.DomainCertStatus == "" || site.DomainCertStatus == "pending") {
			t := site.DomainBoundAt.Time.Add(h.Limits.DomainUnprovenTTL)
			resp.DomainExpiresAt = &t
		}
	}
	return resp
}

func (h *SiteHandler) adminSiteRow(s model.Site) map[string]any {
	var deployed any
	if s.LastDeployedAt.Valid {
		deployed = s.LastDeployedAt.Time
	}
	vis := s.Visibility
	if vis == "" {
		vis = "unlisted"
	}
	m := map[string]any{
		"id":               s.ID,
		"name":             s.Name,
		"site_url":         h.Hosts.Addressing().SiteURLFor(s),
		"active_version":   s.ActiveVersion,
		"custom_domain":    s.CustomDomain.String,
		"domain_status":    s.DomainStatus.String,
		"created_at":       s.CreatedAt,
		"deployed_at":      deployed,
		"visibility":       vis,
		"offline":          s.Offline,
		"passcode":         s.Passcode,
		"access":           web.SiteAccessOf(s),
		"suspended":        s.Suspended(),
		"suspended_reason": s.SuspendedReason(),
		"suspended_by":     web.SuspendedBy(s),
	}
	if host, ok := h.Hosts.SiteFamilyAddress(s.UserID, s.Name); ok {
		m["family_address"] = "https://" + host + "/"
	}
	if addrs := h.Hosts.SiteFamilyAddrs(s.UserID, s.Name); len(addrs) > 0 {
		list := make([]string, 0, len(addrs))
		for _, a := range addrs {
			list = append(list, "https://"+a.Host+"/")
		}
		m["family_addresses"] = list
	}
	return m
}

func (h *SiteHandler) writeUnpublishedState(w http.ResponseWriter, user *model.User, site model.Site, n int, state siteResponseState) {
	note := "Stored as version " + strconv.Itoa(n) + " without making it live; visitors still see version " + strconv.Itoa(site.ActiveVersion) +
		". Make it live with PUT /v1/sites/" + site.Name + "/active-version {\"version_number\": " + strconv.Itoa(n) + "}."
	resp := h.toSiteResponseState(site, note, state)
	resp.FileBytes = state.fileBytes
	resp.UnpublishedVersion = n
	if link, exp, ok := state.previewURL, state.previewExpires, state.previewOK; ok {
		resp.PreviewURL = link
		resp.PreviewExpiresAt = &exp
	} else {
		resp.Note += " No preview link: " + h.noPreviewWhy() + "."
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// writeDeploySiteNotFound answers a deploy (PUT) to a site the account does
// not have. The error text and code stay what clients already read; the hint
// names the exact call that creates it.
func writeDeploySiteNotFound(w http.ResponseWriter, r *http.Request, siteName string) {
	name := hintName(siteName)
	path := "/v1/sites/" + name
	if strings.HasSuffix(r.URL.Path, "/files") {
		path += "/files"
	}
	httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{
		Error: "site not found: create it with POST, or add ?create=1 to this PUT to create it when missing",
		Code:  "not_found",
		Hint:  "No site named " + name + " on your account. To create it, add ?create=1 (PUT " + path + "?create=1) or POST " + path + ".",
	})
}

// writeSiteNotFound answers a call about a site the account does not have
// (versions, rollback, settings). Same "site not found" text as before, plus
// a code and a hint that points at the list of the account's sites.
func writeSiteNotFound(w http.ResponseWriter, siteName string) {
	httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{
		Error: "site not found",
		Code:  "site_not_found",
		Hint:  "No site named " + hintName(siteName) + " on your account. GET /v1/sites lists the sites on your account; to create one, POST /v1/sites/<name>/files.",
	})
}

// hintName is a site name as echoed in a hint: trimmed and at most 63
// characters (the longest valid name), so the answer stays small.
func hintName(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 63 {
		r = r[:63]
	}
	return string(r)
}
