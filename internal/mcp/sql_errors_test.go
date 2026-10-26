package mcp

import (
	"errors"
	"strings"
	"testing"
)

// A storage SQL refusal reaches the agent as the server wrote it, with its
// code, and without a generic "correct the arguments" line on top.
func TestStorageSQLRefusalPassesThroughWithoutGenericHint(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"POST /v1/sites/blog/storage/sqlite/report/query": func() (int, string) {
			return 400, `{"error":"table orders does not exist. Tables here: customers, parents","code":"sql_no_such_table"}`
		},
	}}
	s := newTestServer(up)
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_sql_query", map[string]any{"site": "blog", "name": "report", "sql": "SELECT * FROM orders"}), nil, true))
	if !isErr || !strings.Contains(text, "Tables here: customers, parents") || !strings.Contains(text, "[sql_no_such_table]") {
		t.Fatalf("refusal lost: %s", text)
	}
	if strings.Contains(text, "rejected as invalid") || strings.Contains(text, "\n") {
		t.Fatalf("generic hint added: %s", text)
	}
}

func TestSiteNameErrorsAreActionable(t *testing.T) {
	_, err := siteArg(map[string]any{"site": "https://birthday-rsvp.ann.simple-host.app/"})
	var refusal *refusalError
	if !errors.As(err, &refusal) || refusal.code != "invalid_site_name" || !strings.Contains(err.Error(), `probably "birthday-rsvp"`) {
		t.Fatalf("address as site: %v", err)
	}
	if _, err := siteArg(map[string]any{"site": "My Site"}); err == nil || strings.Contains(err.Error(), "probably") {
		t.Fatalf("display name: %v", err)
	}
	if got := siteNotFoundText("blgo", []string{"blog", "shop"}); !strings.Contains(got, `no site named "blgo"`) || !strings.Contains(got, "Sites here: blog, shop") {
		t.Fatalf("not found: %s", got)
	}
	if got := siteNotFoundText("surveys", []string{"a", "b", "survey", "c"}); !strings.Contains(got, "Sites here: survey, a, b, c") {
		t.Fatalf("closest first: %s", got)
	}
	many := make([]string, 25)
	for i := range many {
		many[i] = "s"
	}
	if got := siteNotFoundText("x", many); !strings.Contains(got, "and 5 more (list_sites shows all)") {
		t.Fatalf("many: %s", got)
	}
	if got := siteNotFoundText("x", nil); !strings.Contains(got, "no sites yet") {
		t.Fatalf("none: %s", got)
	}
}
