package handler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The queue estimate: room now means one timer interval; a full daily cap
// means waiting until the oldest issue of the day leaves the window.
func TestEstimateCertReady(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	l := siteCertLimits{Budget: 40, Daily: 12, PerRun: 6, Every: 10 * time.Minute, RetryIn: 6 * time.Hour}
	if got := estimateCertReady(nil, 0, now, l); got != now.Add(10*time.Minute) {
		t.Fatalf("empty queue: %s", got)
	}
	var day []time.Time
	for i := 0; i < 12; i++ {
		day = append(day, now.Add(-time.Duration(20-i)*time.Hour))
	}
	// First of the 12 was 20 h ago: room again in about 4 h.
	got := estimateCertReady(day, 0, now, l)
	if d := got.Sub(now); d < 4*time.Hour || d > 4*time.Hour+15*time.Minute {
		t.Fatalf("daily cap full: ready in %s", d)
	}
	// Three ahead: the fourth slot opens when the fourth-oldest leaves (17 h ago -> 7 h).
	got = estimateCertReady(day, 3, now, l)
	if d := got.Sub(now); d < 7*time.Hour || d > 7*time.Hour+15*time.Minute {
		t.Fatalf("three ahead: ready in %s", d)
	}
	// Weekly budget spent: waits for the week window.
	var week []time.Time
	for i := 0; i < 40; i++ {
		week = append(week, now.Add(-6*24*time.Hour-time.Duration(i)*time.Minute))
	}
	got = estimateCertReady(week, 0, now, l)
	if d := got.Sub(now); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("weekly budget spent: ready in %s", d)
	}
	// A huge queue and a long log answer quickly, and the same as four
	// weeks' budget.
	var long []time.Time
	for i := 0; i < 5000; i++ {
		long = append(long, now.Add(-30*24*time.Hour-time.Duration(i)*time.Minute))
	}
	start := time.Now()
	capped := estimateCertReady(long, 4*l.Budget, now, l)
	if huge := estimateCertReady(long, 100000, now, l); !huge.Equal(capped) || time.Since(start) > 2*time.Second {
		t.Fatalf("long queue: %s vs %s in %s", huge, capped, time.Since(start))
	}
}

func TestAddressStateFromIssuerFiles(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	okey := map[string]string{"X-API-Key": olive.key}
	if err := os.MkdirAll(filepath.Join(dir, "failed"), 0o755); err != nil {
		t.Fatal(err)
	}
	addressCache.Delete(oh)
	me := func() map[string]any {
		t.Helper()
		addressCache.Delete(oh)
		r := a.at(t, "GET", "simple-host.test", "/v1/me", nil, okey)
		var m map[string]any
		if err := json.Unmarshal(r.body, &m); err != nil {
			t.Fatalf("me: %s", r.body)
		}
		addr, _ := m["address"].(map[string]any)
		return addr
	}
	// Waiting: the request is in the queue; 12 issued in the last hours.
	var log strings.Builder
	now := time.Now()
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&log, "%d h%d\n", now.Add(-time.Duration(20-i)*time.Hour).Unix(), i)
	}
	if err := os.WriteFile(filepath.Join(dir, "issued.log"), []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	addr := me()
	if addr == nil || addr["state"] != "waiting" {
		t.Fatalf("waiting: %v", addr)
	}
	if h, _ := addr["ready_in_hours"].(float64); h < 4 || h > 5 {
		t.Fatalf("ready_in_hours: %v", addr["ready_in_hours"])
	}
	note, _ := addr["note"].(string)
	if !strings.Contains(note, oh+"."+pcSiteDomain+"/<site>/") || !strings.Contains(note, "sign-ins") || !strings.Contains(note, "about 5 hours") {
		t.Fatalf("note: %q", note)
	}
	// The site list carries it on the site, with that site's addresses.
	r := a.at(t, "GET", "simple-host.test", "/v1/sites", nil, okey)
	var listed []struct {
		AddressState map[string]any `json:"address_state"`
	}
	if err := json.Unmarshal(r.body, &listed); err != nil || len(listed) == 0 || listed[0].AddressState["address"] != "https://shop."+oh+"."+pcSiteDomain+"/" ||
		listed[0].AddressState["interim_address"] != "https://"+oh+"."+pcSiteDomain+"/shop/" {
		t.Fatalf("site list: %s", r.body)
	}
	// Failing: the issuer's failure marker, retried after its wait.
	if err := os.WriteFile(filepath.Join(dir, "failed", oh), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if addr = me(); addr["state"] != "failing" || !strings.Contains(addr["note"].(string), "retried automatically") {
		t.Fatalf("failing: %v", addr)
	}
	// Ready: no hours, no note; the site carries nothing.
	markReady(t, dir, oh)
	if addr = me(); addr["state"] != "ready" || addr["note"] != nil || addr["ready_in_hours"] != nil {
		t.Fatalf("ready: %v", addr)
	}
	addressCache.Delete(oh)
	r = a.at(t, "GET", "simple-host.test", "/v1/sites", nil, okey)
	if strings.Contains(string(r.body), "address_state") {
		t.Fatalf("ready site still carries address_state: %s", r.body)
	}
}
