package sql_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"simplehost/core/testkit"
	datasql "simplehost/data/sql"
)

// Review A7: an owner's query answer is cut at the row and byte caps, and
// says so; one value over the byte cap is refused.
func TestQueryAnswerIsCapped(t *testing.T) {
	k := testkit.Start(t, datasql.Mount)
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "shop", map[string]string{"index.html": "shop"})
	run := func(mode, sql string) testkit.Res {
		return testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/sites/shop/storage/sqlite/big/"+mode,
			map[string]any{"sql": sql}, "Authorization", "Bearer "+bob.Key)
	}
	run("execute", "CREATE TABLE t (x)")
	r := run("query", "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT x FROM c")
	var out struct {
		Rows [][]any
		Cut  string
	}
	if json.Unmarshal([]byte(r.Body), &out); r.Status != 200 || len(out.Rows) != 10_000 || out.Cut == "" {
		t.Fatalf("endless rows: %d, %d rows, cut %q", r.Status, len(out.Rows), out.Cut)
	}
	if r := run("query", "SELECT randomblob(9000000)"); r.Status != 413 {
		t.Fatalf("one huge value: %d %.200s", r.Status, r.Body)
	}
	// Round 2: one wide row cannot pass the byte cap, and too wide a row is
	// refused, not a crash. Several at once: the rest are told to wait.
	cols := func(n int, e string) string { return "SELECT " + strings.TrimSuffix(strings.Repeat(e+",", n), ",") }
	if r := run("query", cols(20, "hex(randomblob(400000))")); r.Status != 200 || len(r.Body) > 9<<20 || !strings.Contains(r.Body, `"cut"`) {
		t.Fatalf("one wide row: %d, %d bytes", r.Status, len(r.Body))
	}
	if r := run("query", cols(40, "zeroblob(8000000)")); r.Status != 413 {
		t.Fatalf("40 huge values: %d %.200s", r.Status, r.Body)
	}
	if r := run("query", cols(101, "1")); r.Status != 400 {
		t.Fatalf("101 columns: %d %.200s", r.Status, r.Body)
	}
	var wg sync.WaitGroup
	status := make([]int, 8)
	for i := range status {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := run("query", cols(20, "hex(randomblob(400000))"))
			if status[i] = r.Status; len(r.Body) > 9<<20 {
				status[i] = -1
			}
		}(i)
	}
	wg.Wait()
	for _, s := range status {
		if s != 200 && s != 503 {
			t.Fatalf("8 at once: %v", status)
		}
	}
}
