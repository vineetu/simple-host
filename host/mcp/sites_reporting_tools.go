package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

func siteReportingTools(limits Limits) []Tool {
	return []Tool{
		{
			OutputSchema: outObject(map[string]any{
				"site":       outString(outSiteName),
				"range_days": outInteger("The window the totals cover, in days."),
				"totals":     trafficSplitSchema("Traffic over the whole window. Visitors are unique across the window."),
				"last_24h":   trafficSplitSchema("Traffic over the last 24 hours."),
				"top_pages": outArray("The most viewed pages over the window, people only, most first.", outObject(map[string]any{
					"path":  outString("The page's path on the site."),
					"views": outInteger("Views by people."),
				}, "path", "views")),
				"top_referrers": outArray("Where visitors came from over the window: the referring domain only (never a full address), people only, most first.", outObject(map[string]any{
					"domain": outString("The referring site's domain."),
					"views":  outInteger("Views by people arriving from it."),
				}, "domain", "views")),
			}, "site", "range_days", "totals", "last_24h"),
			Name:        "site_analytics",
			Title:       "How many people visited",
			Description: "Visits to a site over the last N days, split into people (not counting the owner's own visits), bots and monitoring, with the most viewed pages and the domains visitors came from (people only). When asked how many people visited, report the `person` numbers only.",
			InputSchema: object(map[string]any{
				"site": str(siteDesc),
				"days": map[string]any{"type": "integer", "description": "Window in days (1–90, default 30)."},
			}, "site"),
			Annotations: readOnly(),
			run:         runSiteAnalytics,
		},
		{
			policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			OutputSchema: outObject(map[string]any{
				"site":       outString(outSiteName),
				"url":        outString("The download link: a .tar.gz with the site's files and saved data. Give it to the person; it opens without signing in, so do not post it publicly."),
				"expires_at": outString("When the link stops working (RFC 3339), " + span(limits.ExportLinkTTL) + " after it was made."),
			}, "site", "url", "expires_at"),
			Name:  "export_site",
			Title: "Download a copy of a site",
			Description: "Make a download link for a copy of one of the person's sites: a .tar.gz holding its live files and its saved data " +
				"(storage resources with their KV values, SQLite databases, and files). The link works for " + span(limits.ExportLinkTTL) + " and only for that site; " +
				"give it to the person to click, and make a new one if it has expired. Do not post it anywhere public: until it expires, anyone with it can download the copy.",
			InputSchema: object(map[string]any{"site": str(siteDesc)}, "site"),
			// Computes a signed download URL without creating an export job.
			Annotations: readOnly(),
			run: func(c *call, args map[string]any) (output, error) {
				name, err := siteArg(args)
				if err != nil {
					return output{}, err
				}
				var link struct {
					Site      string `json:"site"`
					URL       string `json:"url"`
					ExpiresAt string `json:"expires_at"`
				}
				if err := c.doJSON("export_site", http.MethodPost, "/v1/sites/"+url.PathEscape(name)+"/export-link", nil, &link); err != nil {
					return output{}, err
				}
				if link.URL == "" {
					return output{}, errors.New("export_site failed: the server returned no link; try again in a moment")
				}
				if link.Site == "" {
					link.Site = name
				}
				out := map[string]any{"site": link.Site, "url": link.URL, "expires_at": link.ExpiresAt}
				text := fmt.Sprintf("Download link for a copy of %s (files and saved data), valid until %s: %s", link.Site, link.ExpiresAt, link.URL)
				return output{Text: text, Structured: out}, nil
			},
		},
	}
}

func runSiteAnalytics(c *call, args map[string]any) (output, error) {
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	days, given, err := wholeNumber(args, "days", false, 1, 90)
	if err != nil {
		return output{}, err
	}
	if !given {
		days = 30
	}
	var full map[string]any
	if err := c.doJSON("site_analytics", http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/analytics?days="+strconv.Itoa(days), nil, &full); err != nil {
		return output{}, err
	}
	// Totals only: the daily and hourly series are for charts and
	// would swamp a conversation.
	// The structured split stays person/bot/infra/unknown: the
	// owner's own visits (class owner) are stated in the text.
	out := map[string]any{"site": name, "range_days": full["range_days"], "totals": withoutOwner(full["totals"]), "last_24h": withoutOwner(full["last_24h"])}
	// Top pages and referring domains are a second read; the
	// totals stand without them if it fails.
	if top := c.do(http.MethodGet, "/v1/sites/"+url.PathEscape(name)+"/analytics/top?days="+strconv.Itoa(days), nil, nil); top.ok() {
		var t struct {
			Pages     []any `json:"pages"`
			Referrers []any `json:"referrers"`
		}
		if json.Unmarshal(top.body, &t) == nil && t.Pages != nil && t.Referrers != nil {
			out["top_pages"], out["top_referrers"] = t.Pages, t.Referrers
		}
	}
	text := jsonText(out)
	text += "\n\n" + ownVisitsLine(full["totals"])
	answer := output{Text: text, Structured: out}
	if c.server.dashboardEnabled(c.caller) {
		answer.Meta = map[string]any{"simple-host/daily": full["daily"]}
	}
	return answer, nil
}

// withoutOwner copies an analytics split and drops its owner class, so the
// structured result keeps exactly person, bot, infra and unknown.
func withoutOwner(split any) any {
	m, ok := split.(map[string]any)
	if !ok {
		return split
	}
	copied := make(map[string]any, len(m))
	for k, v := range m {
		if k != "owner" {
			copied[k] = v
		}
	}
	return copied
}

// ownVisitsLine reads the owner class out of the totals split and says it in
// words, so the person hears their own visits were left out of person.
func ownVisitsLine(totals any) string {
	views, visitors := 0.0, 0.0
	if m, ok := totals.(map[string]any); ok {
		if o, ok := m["owner"].(map[string]any); ok {
			views, _ = o["views"].(float64)
			visitors, _ = o["visitors"].(float64)
		}
	}
	if views == 0 {
		return "Your own visits, left out of person: 0 views in this window." + ownVisitsCaveat
	}
	return "Your own visits, left out of person: " + strconv.FormatFloat(views, 'f', -1, 64) + " views from " +
		strconv.FormatFloat(visitors, 'f', -1, 64) + " of your addresses in this window." + ownVisitsCaveat
}
