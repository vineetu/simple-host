package sites

import (
	"database/sql"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

func TestFilterSiteList(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	list := []model.Site{
		{Name: "Beta", CreatedAt: day(1), UpdatedAt: day(5), SummaryTitle: "Bakery menu"},
		{Name: "alpha", CreatedAt: day(3), UpdatedAt: day(4), CustomDomain: sql.NullString{String: "bread.example", Valid: true}},
		{Name: "gamma", CreatedAt: day(2), UpdatedAt: day(6), SummaryText: "Bakery hours"},
	}
	for _, tc := range []struct {
		name, query string
		want        []string
		code        string
	}{
		{"default order", "", []string{"gamma", "Beta", "alpha"}, ""},
		{"order created", "order=created", []string{"alpha", "gamma", "Beta"}, ""},
		{"order name", "order=name", []string{"alpha", "Beta", "gamma"}, ""},
		{"bad order", "order=bogus", nil, "bad_order"},
		{"query words", "query=BAKERY+menu", []string{"Beta"}, ""},
		{"query domain", "query=bread", []string{"alpha"}, ""},
		{"created after", "created_after=2026-10-02", []string{"gamma", "alpha"}, ""},
		{"created before day", "created_before=2026-10-02", []string{"gamma", "Beta"}, ""},
		{"updated before rfc3339", "updated_before=2026-10-05T12:00:00Z", []string{"Beta", "alpha"}, ""},
		{"updated after", "updated_after=2026-10-06", []string{"gamma"}, ""},
		{"limit offset", "limit=1&offset=1", []string{"Beta"}, ""},
		{"offset beyond", "offset=5", []string{}, ""},
		{"bad date", "created_after=Tuesday", nil, "bad_date"},
		{"empty date", "updated_before=", nil, "bad_date"},
		{"zero limit", "limit=0", nil, "bad_pagination"},
		{"negative offset", "offset=-1", nil, "bad_pagination"},
		{"date before pagination", "limit=0&created_after=x", nil, "bad_date"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params, _ := url.ParseQuery(tc.query)
			got, err := FilterSiteList(list, params)
			var siteErr *Error
			if tc.code != "" {
				if !errors.As(err, &siteErr) || siteErr.Code != tc.code || got != nil {
					t.Fatalf("got %v %v, want %s", got, err, tc.code)
				}
				return
			}
			names := []string{}
			for _, site := range got {
				names = append(names, site.Name)
			}
			if err != nil || !reflect.DeepEqual(names, tc.want) {
				t.Fatalf("got %v %v, want %v", names, err, tc.want)
			}
		})
	}
	if list[0].Name != "Beta" {
		t.Fatal("input reordered")
	}
}

// Default order sorts by UpdatedAt alone: a stale LastDeployedAt (set by
// SIM-189's search relevance, unrelated to this sort) must not reorder it.
func TestFilterSiteListSortByUpdatedAtOnly(t *testing.T) {
	now := time.Now()
	list := []model.Site{{Name: "fallback", UpdatedAt: now}, {Name: "active", UpdatedAt: now.Add(time.Hour)}}
	list[1].LastDeployedAt.Valid = true
	list[1].LastDeployedAt.Time = now.Add(-time.Hour)
	got, err := FilterSiteList(list, url.Values{"query": {""}})
	if err != nil || got[0].Name != "active" {
		t.Fatalf("updated_at ordering: %+v %v", got, err)
	}
	for _, key := range []string{"created_after", "created_before", "updated_after", "updated_before"} {
		_, err := FilterSiteList(list, url.Values{key: {"bad"}})
		var siteErr *Error
		if !errors.As(err, &siteErr) || siteErr.Code != "bad_date" || !strings.Contains(siteErr.Message, key) {
			t.Errorf("%s: %+v", key, err)
		}
	}
}
