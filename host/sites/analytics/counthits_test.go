package analytics

import "testing"

func TestCountHits(t *testing.T) {
	const site, hour, day = "s1", "2026-11-20T10:00:00Z", "2026-11-20"
	person := bucketKey{siteID: site, hour: hour, class: ClassPerson}
	bot := bucketKey{siteID: site, hour: hour, class: ClassBot}
	hits := []hit{
		{key: person, ipHash: []byte{1}, ip: "a", path: "/", referrer: "example.com"},
		{key: person, ipHash: []byte{1}, ip: "a", path: "/"},
		{key: person, ipHash: []byte{2}, ip: "b"},
		{key: bot, ipHash: []byte{3}, ip: "c", path: "/", referrer: "example.com"},
	}
	c := countHits(hits, map[string]string{"a": "US", "b": "DE"})

	if c.views[person] != 3 || c.views[bot] != 1 {
		t.Errorf("views = %v", c.views)
	}
	if got := c.geo[geoKey{site, day, "US", ClassPerson}]; got != 2 {
		t.Errorf("US person geo views = %d, want 2", got)
	}
	if got := c.geo[geoKey{site, day, "", ClassBot}]; got != 1 {
		t.Errorf("unknown-country bot geo views = %d, want 1", got)
	}
	if len(c.visitors[person]) != 2 || len(c.visitors[bot]) != 1 {
		t.Errorf("visitors = %v", c.visitors)
	}
	if len(c.touched) != 1 {
		t.Errorf("touched = %v", c.touched)
	}
	// Pages and referrers count people only.
	if got := c.pages[dayItemKey{site, day, "/"}]; got != 2 || len(c.pages) != 1 {
		t.Errorf("pages = %v", c.pages)
	}
	if got := c.refs[dayItemKey{site, day, "example.com"}]; got != 1 || len(c.refs) != 1 {
		t.Errorf("refs = %v", c.refs)
	}
}
