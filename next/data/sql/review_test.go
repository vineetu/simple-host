package sql_test

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"simplehost/core/testkit"
	datasql "simplehost/data/sql"
)

// peak is the process's memory high-water mark in MB.
func peak() int {
	b, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VmHWM:"); ok {
			var kb int
			fmt.Sscanf(strings.TrimSpace(v), "%d", &kb)
			return kb >> 10
		}
	}
	return 0
}

// Each site's database runs in its own sandbox with a fixed memory ceiling:
// a memory-hungry query fails inside its sandbox while another site's query
// answers at the same time, and 40 at once leave the process's memory bounded.
func TestQueryFailsInsideItsSandbox(t *testing.T) {
	k := testkit.Start(t, datasql.Mount)
	ann, bob := k.Person(t, "ann"), k.Person(t, "bob")
	k.Publish(t, ann, "shop", map[string]string{"index.html": "shop"})
	k.Publish(t, bob, "shop", map[string]string{"index.html": "shop"})
	run := func(who testkit.Who, sql string) testkit.Res {
		return testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/sites/shop/storage/sqlite/big/execute",
			map[string]any{"sql": sql}, "Authorization", "Bearer "+who.Key)
	}
	run(ann, "SELECT 1")
	run(bob, "SELECT 1")
	hungry := "SELECT length(group_concat(hex(zeroblob(10000)))) FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c LIMIT 10000) SELECT x FROM c)"
	before := peak()
	var wg sync.WaitGroup
	status := make([]int, 40)
	for i := range status {
		wg.Add(1)
		go func(i int) { defer wg.Done(); status[i] = run(ann, hungry).Status }(i)
	}
	other := run(bob, "SELECT 41 + 1")
	wg.Wait()
	for _, s := range status {
		if s != 413 && s != 503 {
			t.Fatalf("40 hungry queries: %v", status)
		}
	}
	if other.Status != 200 {
		t.Fatalf("another site's query meanwhile: %d %s", other.Status, other.Body)
	}
	t.Logf("40 hungry queries: %v; process peak %d -> %d MB", status, before, peak())
	if grew := peak() - before; grew > 1200 {
		t.Fatalf("40 hungry queries grew the process by %d MB", grew)
	}
	if r := run(ann, "SELECT 2"); r.Status != 200 {
		t.Fatalf("after: %d %s", r.Status, r.Body)
	}
}
