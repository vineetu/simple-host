package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The cost calculator (/costs): its price file, its arithmetic and its links.
// The arithmetic lives in static/costs/calc.js, so the page, the setup helper
// and this test run the same code; the test runs it under node.

var checkedDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// Every price in prices.json names an https source and the day it was
// checked, and the providers the page leads with come first.
func TestCostsPricesFile(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/costs/prices.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("prices.json: %v", err)
	}
	prices := 0
	var walk func(path string, v any)
	walk = func(path string, v any) {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["usd"]; ok {
				prices++
				src, _ := x["source"].(string)
				if !strings.HasPrefix(src, "https://") {
					t.Errorf("%s: price without an https source", path)
				}
				if c, _ := x["checked"].(string); !checkedDateRe.MatchString(c) {
					t.Errorf("%s: price without a checked date", path)
				}
				if per, _ := x["per"].(string); per != "hour" && per != "month" && per != "GB-month" && per != "GB" {
					t.Errorf("%s: unknown unit %q", path, per)
				}
			}
			if _, ok := x["tiers"]; ok && strings.HasSuffix(path, "egress") {
				if src, _ := x["source"].(string); !strings.HasPrefix(src, "https://") {
					t.Errorf("%s: egress tiers without a source", path)
				}
			}
			for k, vv := range x {
				walk(path+"."+k, vv)
			}
		case []any:
			for i, vv := range x {
				walk(path+"["+strconv.Itoa(i)+"]", vv)
			}
		}
	}
	walk("prices", doc)
	if prices < 40 {
		t.Errorf("only %d prices found; the walk is not reading the file", prices)
	}
	var want = []string{"aws", "azure", "gcp"}
	provs, _ := doc["providers"].([]any)
	for i, id := range want {
		p, _ := provs[i].(map[string]any)
		if p["id"] != id || p["group"] != "main" {
			t.Errorf("provider %d = %v (%v), want %s in the main group", i, p["id"], p["group"], id)
		}
		cal, _ := p["calculator"].(map[string]any)
		if u, _ := cal["url"].(string); !strings.HasPrefix(u, "https://") {
			t.Errorf("%s: no calculator link", id)
		}
	}
}

func runCostsNode(t *testing.T, pricesPath, prog string) map[string]any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, err := staticFiles.ReadFile("static/costs/calc.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	calc := filepath.Join(dir, "calc.js")
	if err := os.WriteFile(calc, js, 0o644); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(pricesPath)
	src := "const C = require(" + jsString(calc) + "); const P = JSON.parse(require('fs').readFileSync(" + jsString(abs) + ", 'utf8'));\n" +
		"const out = (function () {" + prog + "})(); process.stdout.write(JSON.stringify(out));"
	cmd := exec.Command(node, "-e", src)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("node output %q: %v", b, err)
	}
	return out
}

func jsString(s string) string { b, _ := json.Marshal(s); return string(b) }

func costsNear(a, b float64) bool { return math.Abs(a-b) < 0.006 }

// The arithmetic, against a fixture whose answers are worked out by hand in
// the comments.
func TestCostsCalcFixture(t *testing.T) {
	out := runCostsNode(t, "testdata/costs-fixture.json", `
	  var r = {};
	  r.a = C.estimate(P, 't', {});
	  r.b = C.estimate(P, 't', { existing: true });
	  r.c = C.estimate(P, 't', { network: 'private' });
	  r.d = C.estimate(P, 't', { traffic: 'x' });
	  r.e = C.estimate(P, 't', { existing: true, ingress: false });
	  r.f = C.estimate(P, 't', { ha: true });
	  return r;`)
	type est struct {
		total, perPerson float64
		cats             map[string]float64
	}
	read := func(k string) est {
		m := out[k].(map[string]any)
		e := est{total: m["total"].(float64), perPerson: m["perPerson"].(float64), cats: map[string]float64{}}
		for c, v := range m["cats"].(map[string]any) {
			e.cats[c] = v.(float64)
		}
		return e
	}
	// A: new cluster, 2,048 views → 3 replicas → 3.5 GiB, 1.25 CPU: one n1
	// (3.5 usable) at 100; control plane 0.5 × 100 h = 50; disk 10 GB × 0.1
	// = 1. 10 people, 3 replicas → the 2 GB database, 20; storage at its 5 GB
	// minimum, 5. Bucket 1,024 sites × 1 MB × 2 versions = 2 GB × 0.5 = 1.
	// Egress 2 GB, 1 free, × 10 = 10. LB 0.1 × 100 = 10, plus 2 GB × 1 = 2.
	a := read("a")
	for c, v := range map[string]float64{"cluster": 150, "other": 1, "database": 25, "storage": 1, "traffic": 10, "lb": 12} {
		if !costsNear(a.cats[c], v) {
			t.Errorf("A %s = %v, want %v", c, a.cats[c], v)
		}
	}
	if !costsNear(a.total, 199) || !costsNear(a.perPerson, 19.9) {
		t.Errorf("A total %v per person %v, want 199 and 19.9", a.total, a.perPerson)
	}
	// B: your cluster, your ingress: pods need 2.5 GiB and 0.75 CPU, cheapest
	// as 0.3125 of an n2 (150) = 46.875; no control plane, disk or LB hour;
	// the LB's 2 GB processed stays.
	b := read("b")
	if !costsNear(b.total, 84.88) || !costsNear(b.cats["cluster"], 46.88) || !costsNear(b.cats["lb"], 2) || b.cats["other"] != 0 {
		t.Errorf("B = %+v, want total 84.88, cluster 46.88, lb 2, other 0", b)
	}
	// C: A over a private link: 2 GB × 0.5 = 1 instead of 10.
	if c := read("c"); !costsNear(c.total, 190) || !costsNear(c.cats["traffic"], 1) {
		t.Errorf("C = %+v, want total 190, traffic 1", c)
	}
	// D: traffic level x: 10 people × 1 page × 10 days = 100 views of 1 MB:
	// 2 replicas, the 1 GB database (10), egress inside the free GB, LB
	// 0.1 GB processed → 0.1.
	if d := read("d"); !costsNear(d.total, 177.1) || d.cats["traffic"] != 0 {
		t.Errorf("D = %+v, want total 177.1, traffic 0", d)
	}
	// E: your cluster without an ingress: B plus a load balancer hour (10).
	if e := read("e"); !costsNear(e.total, 94.88) {
		t.Errorf("E total = %v, want 94.88", e.total)
	}
	// F: A with high availability: control plane 1 × 100, 2 × n1 (3 GiB
	// each) = 200, 2 disks, the HA database 40 and storage 5 × 2.
	if f := read("f"); !costsNear(f.total, 100+200+2+40+10+1+10+12) {
		t.Errorf("F total = %v, want %v", f.total, 100+200+2+40+10+1+10+12)
	}
}

// The real price file: every provider prices every input without error, and
// the directions hold (a cluster you have costs less than a new one, heavy
// traffic more than typical, a private link less than the internet on the
// big three once traffic is past the free tiers).
func TestCostsCalcRealPrices(t *testing.T) {
	out := runCostsNode(t, "static/costs/prices.json", `
	  var bad = [], r = {};
	  P.providers.forEach(function (p) {
	    [10, 100, 2000].forEach(function (people) {
	      var own = C.estimate(P, p.id, { people: people, sites: people * 3 });
	      var fresh = C.estimate(P, p.id, { people: people, sites: people * 3, existing: false });
	      var heavy = C.estimate(P, p.id, { people: people, sites: people * 3, traffic: 'heavy', network: 'internet' });
	      var priv = C.estimate(P, p.id, { people: people, sites: people * 3, traffic: 'heavy', network: 'private' });
	      [own, fresh, heavy, priv].forEach(function (e) { if (!(e.total > 0) || !isFinite(e.perPerson)) bad.push(p.id + ' bad total'); });
	      if (!(own.total < fresh.total)) bad.push(p.id + ' own cluster not cheaper');
	      if (people >= 100 && !(heavy.total >= own.total)) bad.push(p.id + ' heavy not dearer');
	      if (people >= 100 && p.private_link && !(priv.total < heavy.total)) bad.push(p.id + ' private link not cheaper');
	    });
	  });
	  r.bad = bad;
	  r.dates = C.checkedDates(P);
	  return r;`)
	if bad := out["bad"].([]any); len(bad) > 0 {
		t.Errorf("real prices: %v", bad)
	}
	if n := len(out["dates"].([]any)); n < 40 {
		t.Errorf("checkedDates found %d dates", n)
	}
}

// /costs serves the page with its scripts, and the pages that should link
// to it do.
func TestCostsPageAndLinks(t *testing.T) {
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-host.app", "/costs")
	if rec.Code != http.StatusOK {
		t.Fatalf("/costs = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`src="/costs/calc.js"`, `src="/costs/costs.js"`, `id="in-existing"`, `id="in-ingress"`, `id="in-private"`, `id="in-traffic"`, `id="example"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/costs lacks %s", want)
		}
	}
	if strings.Contains(body, "<!--sh:ask") {
		t.Error("/costs carries an Ask marker")
	}
	for _, path := range []string{"/costs/prices.json", "/costs/calc.js", "/costs/costs.js"} {
		if rec := get(t, mux, "simple-host.app", path); rec.Code != http.StatusOK {
			t.Errorf("%s = %d", path, rec.Code)
		}
	}
	for _, f := range []string{"static/enterprise.html", "static/enterprise-brief.html", "static/setup/setup.js"} {
		b, err := staticFiles.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "/costs") {
			t.Errorf("%s does not link /costs", f)
		}
	}
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "simple-host.app/costs") {
		t.Error("README does not link the cost calculator")
	}
}

// The ranges the enterprise pages and the enterprise Ask pack quote come from
// the calculator (calc.js headline over prices.json): when prices change,
// this names the text to change.
func TestCostsHeadlineMatchesPages(t *testing.T) {
	out := runCostsNode(t, "static/costs/prices.json", `
	  var r = {};
	  P.model.headline.people.forEach(function (n) { r[n] = C.headline(P, n); });
	  return r;`)
	cents := func(usd float64) string {
		return strconv.FormatFloat(math.Round(usd*1000)/10, 'f', -1, 64)
	}
	h2000 := out["2000"].(map[string]any)
	h40 := out["40"].(map[string]any)
	span := func(h map[string]any) string {
		return "$" + strconv.Itoa(int(h["low"].(float64))) + "–" + strconv.Itoa(int(h["high"].(float64)))
	}
	big := span(h2000) + " a month"
	per := cents(h2000["perLow"].(float64)) + "–" + cents(h2000["perHigh"].(float64)) + " cents a person"
	small := span(h40) + " a month"
	files := map[string][]string{
		"static/enterprise.html":       {big, per, small},
		"static/enterprise-brief.html": {span(h2000) + " a month", per},
		"askdata/enterprise.txt":       {big, per, small},
	}
	for f, wants := range files {
		var b []byte
		var err error
		if strings.HasPrefix(f, "static/") {
			b, err = staticFiles.ReadFile(f)
		} else {
			b, err = os.ReadFile(f)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range wants {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s should say %q (from prices.json)", f, w)
			}
		}
	}
}
