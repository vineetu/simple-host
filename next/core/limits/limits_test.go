package limits_test

import (
	"strings"
	"testing"

	"simplehost/core/limits"
	"simplehost/core/testkit"
)

// SPEC test 36 (F31): a flood from one address is slowed, naming the limit,
// and only that address. The owner's own (operator) account is not limited.
func TestFloodIsSlowedButNotTheOperator(t *testing.T) {
	k := testkit.Start(t)
	b := k.Browser()
	health := "https://simple-host.app/health"
	for i := int64(0); i < limits.Requests.N; i++ {
		if r := testkit.Call(t, b, "GET", health, nil, "X-Real-IP", "203.0.113.7"); r.Status != 200 {
			t.Fatalf("request %d: %d %s", i, r.Status, r.Body)
		}
	}
	r := testkit.Call(t, b, "GET", health, nil, "X-Real-IP", "203.0.113.7")
	if r.Status != 429 || !strings.Contains(r.Body, "limit_requests") || r.Header.Get("Retry-After") == "" {
		t.Fatalf("flood: %d %s, want 429 naming limit_requests", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", health, nil, "X-Real-IP", "198.51.100.9"); r.Status != 200 {
		t.Fatalf("another address: %d", r.Status)
	}
	boss := k.Person(t, "boss")
	k.Exec(t, `UPDATE people SET operator = true WHERE id = $1`, boss.ID)
	for i := int64(0); i < limits.Requests.N+10; i++ {
		r := testkit.Call(t, b, "GET", health, nil, "X-Real-IP", "203.0.113.7", "Authorization", "Bearer "+boss.Key)
		if r.Status != 200 {
			t.Fatalf("operator request %d: %d %s", i, r.Status, r.Body)
		}
	}
}
