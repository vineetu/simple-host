package web

import (
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The cost calculator (/costs): its price file, its arithmetic and its links.
// The arithmetic lives in static/costs/calc.js, so the page, the setup helper
// and this test run the same code; the test runs it under node.

func runCostsNode(t *testing.T, pricesPath, prog string) map[string]any {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, err := StaticFiles.ReadFile("static/costs/calc.js")
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
