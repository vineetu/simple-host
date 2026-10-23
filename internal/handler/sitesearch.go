package handler

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// filterSiteList leaves the existing no-argument order and size untouched.
func filterSiteList(sites []db.Site, params url.Values) ([]db.Site, *errorResponse) {
	active := false
	for _, key := range []string{"query", "created_after", "created_before", "updated_after", "updated_before", "limit", "offset"} {
		active = active || params.Has(key)
	}
	if !active {
		return sites, nil
	}
	type dateBound struct {
		key string
		at  time.Time
	}
	var bounds []dateBound
	for _, key := range []string{"created_after", "created_before", "updated_after", "updated_before"} {
		if !params.Has(key) {
			continue
		}
		at, err := time.Parse(time.RFC3339, params.Get(key))
		if err != nil {
			at, err = time.Parse("2006-01-02", params.Get(key))
			if err != nil {
				return nil, &errorResponse{Error: key + " must be RFC3339 or YYYY-MM-DD", Code: "bad_date"}
			}
			if strings.HasSuffix(key, "_before") {
				at = at.AddDate(0, 0, 1).Add(-time.Nanosecond)
			}
		}
		bounds = append(bounds, dateBound{key, at})
	}
	limit, offset := 200, 0
	for _, p := range []struct {
		key  string
		dest *int
	}{{"limit", &limit}, {"offset", &offset}} {
		if !params.Has(p.key) {
			continue
		}
		n, err := strconv.Atoi(params.Get(p.key))
		if err != nil || n < 0 {
			return nil, &errorResponse{Error: p.key + " must be a non-negative integer", Code: "bad_pagination"}
		}
		*p.dest = n
	}
	if limit > 500 {
		limit = 500
	}
	words := strings.Fields(strings.ToLower(params.Get("query")))
	matches := make([]db.Site, 0, len(sites))
	for _, site := range sites {
		haystack := strings.ToLower(strings.Join([]string{site.Name, site.CustomDomain.String, site.SummaryTitle, site.SummaryDescription, site.SummaryHeadings, site.SummaryText}, " "))
		match := true
		for _, word := range words {
			if !strings.Contains(haystack, word) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		for _, bound := range bounds {
			at := site.CreatedAt
			if strings.HasPrefix(bound.key, "updated_") {
				at = site.UpdatedAt
			}
			if strings.HasSuffix(bound.key, "_after") && at.Before(bound.at) || strings.HasSuffix(bound.key, "_before") && at.After(bound.at) {
				match = false
				break
			}
		}
		if match {
			matches = append(matches, site)
		}
	}
	deployedAt := func(s db.Site) time.Time {
		if s.LastDeployedAt.Valid {
			return s.LastDeployedAt.Time
		}
		return s.UpdatedAt
	}
	sort.SliceStable(matches, func(i, j int) bool { return deployedAt(matches[i]).After(deployedAt(matches[j])) })
	if offset >= len(matches) {
		return matches[:0], nil
	}
	matches = matches[offset:]
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}
