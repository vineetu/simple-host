package sql_test

import (
	"encoding/json"
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
}
