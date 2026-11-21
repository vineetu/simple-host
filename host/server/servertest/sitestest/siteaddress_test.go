package sitestest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites"
)

func TestAddressStateFromIssuerFiles(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	_, oh := a.UserID(t, olive)
	okey := map[string]string{"X-API-Key": olive.Key}
	if err := os.MkdirAll(filepath.Join(dir, "failed"), 0o755); err != nil {
		t.Fatal(err)
	}
	sites.ForgetAddressState(oh)
	me := func() map[string]any {
		t.Helper()
		sites.ForgetAddressState(oh)
		r := a.At(t, "GET", "simple-host.test", "/v1/me", nil, okey)
		var m map[string]any
		if err := json.Unmarshal(r.Body, &m); err != nil {
			t.Fatalf("me: %s", r.Body)
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
	if !strings.Contains(note, oh+"."+servertest.PcSiteDomain+"/<site>/") || !strings.Contains(note, "sign-ins") || !strings.Contains(note, "about 5 hours") {
		t.Fatalf("note: %q", note)
	}
	// The site list carries it on the site, with that site's addresses.
	r := a.At(t, "GET", "simple-host.test", "/v1/sites", nil, okey)
	var listed []struct {
		AddressState map[string]any `json:"address_state"`
	}
	if err := json.Unmarshal(r.Body, &listed); err != nil || len(listed) == 0 || listed[0].AddressState["address"] != "https://shop."+oh+"."+servertest.PcSiteDomain+"/" ||
		listed[0].AddressState["interim_address"] != "https://"+oh+"."+servertest.PcSiteDomain+"/shop/" {
		t.Fatalf("site list: %s", r.Body)
	}
	// Failing: the issuer's failure marker, retried after its wait.
	if err := os.WriteFile(filepath.Join(dir, "failed", oh), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if addr = me(); addr["state"] != "failing" || !strings.Contains(addr["note"].(string), "retried automatically") {
		t.Fatalf("failing: %v", addr)
	}
	// Ready: no hours, no note; the site carries nothing.
	servertest.MarkReady(t, dir, oh)
	if addr = me(); addr["state"] != "ready" || addr["note"] != nil || addr["ready_in_hours"] != nil {
		t.Fatalf("ready: %v", addr)
	}
	sites.ForgetAddressState(oh)
	r = a.At(t, "GET", "simple-host.test", "/v1/sites", nil, okey)
	if strings.Contains(string(r.Body), "address_state") {
		t.Fatalf("ready site still carries address_state: %s", r.Body)
	}
}
