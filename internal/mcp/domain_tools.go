package mcp

// Access family: custom domains and account address families.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func domainTools(limits Limits) []Tool {
	return []Tool{
		{
			Name:  "connect_domain",
			Title: "Give a site a short free address or its own domain",
			Description: "Connect a custom domain or claim a short simple-host.app address (optional: every site already has its own at https://<site>.<handle>.simple-host.app/). Either a free `<name>.simple-host.app` address (e.g. `clay-studio.simple-host.app`): active at once, no DNS step, first come first served. " +
				"Or the person's own domain (e.g. `rsvp.example.com` or `example.com`): returns the two DNS records they must add at their domain registrar, the address record (dns_record) and a TXT ownership record (ownership_record, to keep in place); relay both exactly, then check with domain_status until it is active. " +
				"Connecting a domain already bound to another of the person's sites moves it to this site. Once active the site lives only at that address, its old address redirects there, and visitors sign in and save there. " +
				"Or `*.<their domain>` (e.g. `*.trips.example.com`, no `site` needed): an address family for the whole account, so every site X answers at X.trips.example.com once the wildcard DNS record and the TXT ownership record are seen and the operator's wildcard certificate is set up; relay the records and check with domain_status (domain `*.trips.example.com`). Ask before connecting one: it applies to every site of the account.",
			InputSchema: object(map[string]any{
				"site":   str(siteDesc + " Not used for a `*.<domain>` address family."),
				"domain": str("The address without https://: a free `<name>.simple-host.app`, the person's own domain or subdomain, e.g. `rsvp.example.com`, or `*.<domain>` for an address family covering every site of the account."),
			}, "domain"),
			// Reaches an arbitrary outside domain and, once DNS proves it,
			// serves the site there, replacing any existing binding for this account.
			Annotations: writes(true, true, true),
			run: func(c *call, args map[string]any) (output, error) {
				domain, err := stringArg(args, "domain")
				if err != nil {
					return output{}, err
				}
				if suffix, ok := familyDomain(domain); ok {
					body, _ := json.Marshal(map[string]string{"suffix": suffix})
					res := c.do(http.MethodPost, "/v1/me/address-families", body, nil)
					if !res.ok() {
						return output{}, restError("connect_domain", res)
					}
					out := familySummary(res.body, limits)
					return output{Text: jsonText(out), Structured: out}, nil
				}
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				body, _ := json.Marshal(map[string]string{"domain": domain})
				res := c.do(http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/domain", body, nil)
				if !res.ok() {
					return output{}, restError("connect_domain", res)
				}
				out := domainSummary(name, res.body, limits)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "domain_status",
			Title: "Check a custom domain",
			Description: "Check whether a site's custom domain is connected: pending (the DNS records are not seen yet, or its certificate is being issued) or active. Shows the certificate's progress and, while pending, the earlier address the site is still served at. " +
				"With domain `*.<domain>`, checks the account's address family instead (no site needed).",
			InputSchema: object(map[string]any{
				"site":   str(siteDesc + " Not used with a `*.<domain>` domain."),
				"domain": str("Optional: `*.<domain>` to check an address family of the account."),
			}),
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				if d, _ := args["domain"].(string); strings.TrimSpace(d) != "" {
					if suffix, ok := familyDomain(d); ok {
						res := c.do(http.MethodGet, "/v1/me/address-families/"+url.PathEscape(suffix), nil, nil)
						if !res.ok() {
							return output{}, restError("domain_status", res)
						}
						out := familySummary(res.body, limits)
						return output{Text: jsonText(out), Structured: out}, nil
					}
				}
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				res := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/domain", nil, nil)
				if !res.ok() {
					return output{}, restError("domain_status", res)
				}
				out := domainSummary(name, res.body, limits)
				return output{Text: jsonText(out), Structured: out}, nil
			},
		},
		{
			Name:  "remove_domain",
			Title: "Disconnect a domain",
			Description: "Disconnect a site's custom domain or free `<name>.simple-host.app` address. Only call this after the person has explicitly confirmed, in this conversation, that they want this specific address disconnected. " +
				"Pass the address being removed as `confirm_domain` (domain_status shows it). Afterwards the site is served at its own address again (or, if the removed domain was still pending, at the earlier address it was still using). " +
				"Links to a removed custom domain stop working; a removed free name keeps redirecting to the site and cannot be claimed by anyone else. " +
				"With confirm_domain `*.<domain>` it disconnects the account's address family (no site needed): every site stops answering under it and goes back to its own address.",
			InputSchema: object(map[string]any{
				"site":           str(siteDesc + " Not used for a `*.<domain>` address family."),
				"confirm_domain": str("The address being disconnected, typed out, as confirmation, e.g. `rsvp.example.com` or `*.trips.example.com`."),
			}, "confirm_domain"),
			// Re-addresses a public site, and a disconnected custom domain can
			// be claimed by someone else, so it is destructive; calling it
			// again would disconnect the next address, so not idempotent.
			Annotations: writes(true, false, true),
			run: func(c *call, args map[string]any) (output, error) {
				confirm, err := stringArg(args, "confirm_domain")
				if err != nil {
					return output{}, err
				}
				if suffix, ok := familyDomain(confirm); ok {
					res := c.do(http.MethodDelete, "/v1/me/address-families/"+url.PathEscape(suffix), nil, nil)
					if !res.ok() {
						return output{}, restError("remove_domain", res)
					}
					out := map[string]any{"removed": "*." + suffix}
					return output{Text: "Disconnected the address family *." + suffix + ". Every site answers at its own address again.", Structured: out}, nil
				}
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				path := "/v1/sites/" + url.PathEscape(name) + "/domain"
				res := c.do(http.MethodGet, path, nil, nil)
				if !res.ok() {
					return output{}, restError("remove_domain", res)
				}
				var cur struct {
					Domain *string `json:"domain"`
				}
				_ = json.Unmarshal(res.body, &cur)
				if cur.Domain == nil || *cur.Domain == "" {
					return output{}, fmt.Errorf("site %q has no domain connected; nothing was changed", name)
				}
				want := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(confirm)), "https://"), "http://"), "/")
				if want != strings.ToLower(*cur.Domain) {
					return output{}, fmt.Errorf("confirm_domain %q does not match the connected domain %q; nothing was changed", confirm, *cur.Domain)
				}
				// The server drops only this address: if the site's domain
				// changed since the read above, it answers domain_changed.
				res = c.do(http.MethodDelete, path+"?domain="+url.QueryEscape(*cur.Domain), nil, nil)
				if !res.ok() {
					return output{}, restError("remove_domain", res)
				}
				out := map[string]any{"site": name, "removed": *cur.Domain}
				if site, err := c.findSite(name); err == nil {
					out["url"] = site.liveURL()
				}
				text := "Disconnected " + *cur.Domain + " from " + name + "."
				if u, ok := out["url"].(string); ok && u != "" {
					text += " The site is now at " + u
				}
				return output{Text: text, Structured: out}, nil
			},
		},
	}
}

// domainSummary is what a person needs about a site's custom domain: which
// domain, whether it is live, the one DNS record to add, and why it is not live
// yet. Binding times and the name of any site a pending binding was taken over
// from are the server's bookkeeping and stay out.
func domainSummary(site string, body []byte, limits Limits) map[string]any {
	var d struct {
		Domain         *string `json:"domain"`
		Status         *string `json:"status"`
		LastError      string  `json:"last_error"`
		Certificate    string  `json:"certificate_status"`
		PreviousDomain string  `json:"previous_domain"`
		FailingSince   string  `json:"failing_since"`
		DNS            *struct {
			Type  string `json:"type"`
			Host  string `json:"host"`
			Value string `json:"value"`
		} `json:"dns"`
		TXT *struct {
			Type  string `json:"type"`
			Host  string `json:"host"`
			Value string `json:"value"`
		} `json:"dns_txt"`
		PartnerDomain string `json:"partner_domain"`
		PartnerStatus string `json:"partner_status"`
		PartnerNote   string `json:"partner_note"`
		PartnerDNS    *struct {
			Type  string `json:"type"`
			Host  string `json:"host"`
			Value string `json:"value"`
		} `json:"dns_partner"`
	}
	_ = json.Unmarshal(body, &d)
	out := map[string]any{"site": site}
	if d.Domain == nil || *d.Domain == "" {
		out["domain"] = nil
		out["status"] = "none"
		return out
	}
	out["domain"] = *d.Domain
	if d.Status != nil {
		out["status"] = *d.Status
	}
	if d.DNS != nil {
		out["dns_record"] = map[string]any{"type": d.DNS.Type, "host": d.DNS.Host, "value": d.DNS.Value}
	}
	if d.TXT != nil {
		out["ownership_record"] = map[string]any{"type": d.TXT.Type, "host": d.TXT.Host, "value": d.TXT.Value}
	}
	if d.PartnerDomain != "" && d.PartnerDNS != nil {
		partner := map[string]any{
			"domain":     d.PartnerDomain,
			"status":     d.PartnerStatus,
			"dns_record": map[string]any{"type": d.PartnerDNS.Type, "host": d.PartnerDNS.Host, "value": d.PartnerDNS.Value},
		}
		if d.PartnerNote != "" {
			partner["note"] = d.PartnerNote
		}
		out["partner"] = partner
	}
	if d.LastError != "" {
		out["last_check"] = d.LastError
	}
	if d.Certificate != "" {
		out["certificate"] = d.Certificate
	}
	if d.PreviousDomain != "" {
		out["serving_at"] = "https://" + d.PreviousDomain + "/"
	}
	if d.FailingSince != "" {
		out["failing_since"] = d.FailingSince
	}
	if d.Status != nil && *d.Status == "active" {
		out["url"] = "https://" + *d.Domain + "/"
	}
	if d.Status != nil && *d.Status == "pending" {
		out["note"] = "Add both DNS records at the domain's registrar within " + span(limits.DomainUnprovenTTL) + ": dns_record points the domain here, and ownership_record (a TXT record) proves it is the person's; keep the TXT record in place afterwards. Until both are seen, the binding is provisional. Then the certificate is issued automatically and the domain goes live within minutes."
		if _, ok := out["partner"]; ok {
			out["note"] = out["note"].(string) + " Also add partner.dns_record so " + d.PartnerDomain + " forwards to " + *d.Domain + " (the one ownership record covers both)."
		}
	}
	return out
}

// familyDomain: d names an address family (*.<suffix>); returns the suffix.
func familyDomain(d string) (string, bool) {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	d = strings.TrimSuffix(d, "/")
	suffix, ok := strings.CutPrefix(d, "*.")
	if !ok || suffix == "" {
		return "", false
	}
	return suffix, true
}

// familySummary is what a person needs about an address family.
func familySummary(body []byte, limits Limits) map[string]any {
	var f struct {
		Family     string `json:"family"`
		Suffix     string `json:"suffix"`
		SitePrefix string `json:"site_prefix"`
		Canonical  bool   `json:"canonical"`
		Status     string `json:"status"`
		Live       bool   `json:"live"`
		LastError  string `json:"last_error"`
		ExampleURL string `json:"example_url"`
		DNS        *struct {
			Type, Host, Value string
		} `json:"dns"`
		TXT *struct {
			Type, Host, Value string
		} `json:"dns_txt"`
		Certificate struct {
			Status string `json:"status"`
			Note   string `json:"note"`
		} `json:"certificate"`
	}
	_ = json.Unmarshal(body, &f)
	out := map[string]any{"domain": f.Family, "status": f.Status}
	if f.DNS != nil {
		out["dns_record"] = map[string]any{"type": f.DNS.Type, "host": f.DNS.Host, "value": f.DNS.Value}
	}
	if f.TXT != nil {
		out["ownership_record"] = map[string]any{"type": f.TXT.Type, "host": f.TXT.Host, "value": f.TXT.Value}
	}
	if f.Certificate.Status != "" {
		out["certificate"] = f.Certificate.Status
	}
	if f.LastError != "" {
		out["last_check"] = f.LastError
	}
	fam := map[string]any{"site_prefix": f.SitePrefix, "main_address": f.Canonical, "live": f.Live}
	if f.ExampleURL != "" {
		fam["example_url"] = f.ExampleURL
	}
	if f.Certificate.Note != "" {
		fam["certificate_note"] = f.Certificate.Note
	}
	out["family"] = fam
	if f.Live && f.ExampleURL != "" {
		out["url"] = f.ExampleURL
	}
	if f.Status == "pending" {
		out["note"] = "Add both DNS records at the domain's registrar within " + span(limits.FamilyUnprovenTTL) + ": dns_record (the wildcard) points every name under " + f.Suffix + " here, and ownership_record (a TXT record) proves it is the person's; keep the TXT record in place. Then every site X of the account answers at X." + f.Suffix + " once its certificate is live."
	}
	return out
}
