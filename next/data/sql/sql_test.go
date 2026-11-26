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
			map[string]any{"sql": sql}, "X-API-Key", bob.Key)
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

// query only reads: a statement that would change anything is refused, so
// the AI's query_sql is truly read-only.
func TestQueryOnlyReads(t *testing.T) {
	k := testkit.Start(t, datasql.Mount)
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "shop", map[string]string{"index.html": "shop"})
	run := func(mode, sql string) testkit.Res {
		return testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/sites/shop/storage/sqlite/orders/"+mode,
			map[string]any{"sql": sql}, "X-API-Key", bob.Key)
	}
	if r := run("schema", ""); r.Status == 200 {
		t.Fatalf("the table list made a namespace: %d %s", r.Status, r.Body)
	}
	run("execute", "CREATE TABLE t (x INTEGER)")
	for _, sql := range []string{"INSERT INTO t VALUES (1)", "DELETE FROM t RETURNING x", "WITH a AS (SELECT 1) DELETE FROM t", "DROP TABLE t", "SELECT 1; DELETE FROM t"} {
		if r := run("query", sql); r.Status != 400 || !strings.Contains(r.Body, "not_a_read") {
			t.Fatalf("%s: %d %s", sql, r.Status, r.Body)
		}
	}
	if r := run("query", "WITH a AS (SELECT 2 AS y) SELECT y FROM a"); r.Status != 200 || !strings.Contains(r.Body, "[[2]]") {
		t.Fatalf("a read: %d %s", r.Status, r.Body)
	}
}
