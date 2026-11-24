package sql_test

import (
	"strings"
	"testing"
	"time"

	"simplehost/core/limits"
	"simplehost/core/testkit"
	datasql "simplehost/data/sql"
)

// SPEC test 21 (F15): a query over the time limit and a write over the size
// cap are refused, naming the guard.
func TestTheTwoGuards(t *testing.T) {
	per, bytes := limits.QueryTime.Per, limits.DataBytes.N
	limits.QueryTime.Per, limits.DataBytes.N = 300*time.Millisecond, 1<<20
	t.Cleanup(func() { limits.QueryTime.Per, limits.DataBytes.N = per, bytes })
	k := testkit.Start(t, datasql.Mount)
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "shop", map[string]string{"index.html": "shop"})
	run := func(mode, sql string) testkit.Res {
		return testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/sites/shop/storage/sqlite/big/"+mode,
			map[string]any{"sql": sql}, "Authorization", "Bearer "+bob.Key)
	}
	if r := run("execute", "CREATE TABLE blobs (b BLOB)"); r.Status != 200 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	if r := run("query", "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c"); r.Status != 408 || !strings.Contains(r.Body, "limit_query_time") {
		t.Fatalf("slow query: %d %s", r.Status, r.Body)
	}
	if r := run("execute", "WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 2000) INSERT INTO blobs SELECT randomblob(1000) FROM c"); r.Status != 413 || !strings.Contains(r.Body, "limit_data_size") {
		t.Fatalf("big write: %d %s", r.Status, r.Body)
	}
	if r := run("query", "SELECT count(*) FROM blobs"); r.Status != 200 || !strings.Contains(r.Body, "[[0]]") {
		t.Fatalf("after: %d %s", r.Status, r.Body)
	}
}
