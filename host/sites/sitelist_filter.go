package sites

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

var siteListFilterKeys = []string{"query", "created_after", "created_before", "updated_after", "updated_before", "limit", "offset"}

// siteDateBound is one created_/updated_ _after/_before parameter.
type siteDateBound struct {
	updated, after bool
	at             time.Time
}

// FilterSiteList always sorts (default: newest edited first), and additionally
// filters and pages the result when a query, date bound, limit or offset
// parameter is present; the no-argument size is otherwise untouched.
// Errors are *Error with codes bad_order, bad_date and bad_pagination.
func FilterSiteList(list []model.Site, params url.Values) ([]model.Site, error) {
	sorted, err := orderSites(list, params.Get("order"))
	if err != nil || !siteListFiltered(params) {
		return sorted, err
	}
	bounds, err := siteDateBounds(params)
	if err != nil {
		return nil, err
	}
	limit, offset, err := sitePage(params)
	if err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(params.Get("query")))
	matches := make([]model.Site, 0, len(sorted))
	for _, site := range sorted {
		if siteHasWords(site, words) && siteWithinBounds(site, bounds) {
			matches = append(matches, site)
		}
	}
	if offset >= len(matches) {
		return matches[:0], nil
	}
	matches = matches[offset:]
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

func siteListFiltered(params url.Values) bool {
	for _, key := range siteListFilterKeys {
		if params.Has(key) {
			return true
		}
	}
	return false
}

func siteDateBounds(params url.Values) ([]siteDateBound, error) {
	var bounds []siteDateBound
	for _, key := range []string{"created_after", "created_before", "updated_after", "updated_before"} {
		if !params.Has(key) {
			continue
		}
		after := strings.HasSuffix(key, "_after")
		at, err := time.Parse(time.RFC3339, params.Get(key))
		if err != nil {
			at, err = time.Parse("2006-01-02", params.Get(key))
			if err != nil {
				return nil, &Error{Code: "bad_date", Message: key + " must be RFC3339 or YYYY-MM-DD"}
			}
			if !after {
				at = at.AddDate(0, 0, 1).Add(-time.Nanosecond)
			}
		}
		bounds = append(bounds, siteDateBound{updated: strings.HasPrefix(key, "updated_"), after: after, at: at})
	}
	return bounds, nil
}

func sitePage(params url.Values) (int, int, error) {
	limit, ok := httpx.Limit(params, 200, 500)
	offset, err := strconv.Atoi(params.Get("offset"))
	if params.Get("offset") == "" {
		offset, err = 0, nil
	}
	if !ok || err != nil || offset < 0 {
		return 0, 0, &Error{Code: "bad_pagination", Message: "limit must be 1 to 500 and offset a non-negative integer"}
	}
	return limit, offset, nil
}

func siteHasWords(site model.Site, words []string) bool {
	haystack := strings.ToLower(strings.Join([]string{site.Name, site.CustomDomain.String, site.SummaryTitle, site.SummaryDescription, site.SummaryHeadings, site.SummaryText}, " "))
	for _, word := range words {
		if !strings.Contains(haystack, word) {
			return false
		}
	}
	return true
}

func siteWithinBounds(site model.Site, bounds []siteDateBound) bool {
	for _, bound := range bounds {
		at := site.CreatedAt
		if bound.updated {
			at = site.UpdatedAt
		}
		if bound.after && at.Before(bound.at) || !bound.after && at.After(bound.at) {
			return false
		}
	}
	return true
}

// orderSites sorts a copy of list by the requested key: "updated" (default,
// last edited first, ties broken by created descending), "created" (newest
// created first), or "name" (alphabetical). The input is never mutated.
func orderSites(list []model.Site, order string) ([]model.Site, error) {
	sorted := make([]model.Site, len(list))
	copy(sorted, list)
	switch order {
	case "", "updated":
		sort.SliceStable(sorted, func(i, j int) bool {
			if !sorted[i].UpdatedAt.Equal(sorted[j].UpdatedAt) {
				return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
			}
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		})
	case "created":
		sort.SliceStable(sorted, func(i, j int) bool {
			if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
				return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
			}
			return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name)
		})
	case "name":
		sort.SliceStable(sorted, func(i, j int) bool { return strings.ToLower(sorted[i].Name) < strings.ToLower(sorted[j].Name) })
	default:
		return nil, &Error{Code: "bad_order", Message: "order must be updated, created, or name"}
	}
	return sorted, nil
}
