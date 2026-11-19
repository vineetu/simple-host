package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func runtimeKVListFixture(t *testing.T) (*sqlite3.Conn, []KVEntry) {
	t.Helper()
	keys := []string{"", "%", "%one", "A", "AA", "a", "aa", "_", "_one", `\`, `\one`, "x%_\\", "x%_\\one", "xAAone", "y", "z", "é", "éa", "ê", "ÿ", "ÿa", "中", "中文", "😀", "😀a", "\U0010ffff", "\U0010ffffz"}
	sort.Strings(keys)
	path := filepath.Join(t.TempDir(), "list.sqlite")
	conn, done, err := OpenRuntimeKV(context.Background(), path, true, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(done)
	if err = conn.Exec("BEGIN"); err != nil {
		t.Fatal(err)
	}
	var entries []KVEntry
	for i, key := range keys {
		writer := []string{"", "alice", "bob"}[i%3]
		value := fmt.Sprint(i)
		if err = RuntimeKVPut(conn, key, value, writer, false, 0); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, KVEntry{key, json.RawMessage(value), writer})
	}
	if err = conn.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	return conn, entries
}

func TestRuntimeKVListPrefixRangeEquivalence(t *testing.T) {
	conn, entries := runtimeKVListFixture(t)
	for _, prefix := range []string{"", "%", "_", `\`, "A", "a", "x%_\\", "missing", "é", "ÿ", "中", "😀", "\U0010ffff"} {
		t.Run(fmt.Sprintf("prefix=%q", prefix), func(t *testing.T) {
			for _, after := range []string{"", "A", "a", "z", "é", "éa", "ÿ", "中文", "\U0010ffffz"} {
				for _, filter := range []string{"", "alice", "bob", "absent"} {
					for _, limit := range []int{0, 1, 100} {
						got, err := RuntimeKVList(conn, prefix, after, limit, filter)
						if err != nil {
							t.Fatal(err)
						}
						want := []KVEntry{}
						for _, e := range entries {
							if len(want) < limit && strings.HasPrefix(e.Key, prefix) && e.Key > after && (filter == "" || e.VisitorID == filter) {
								want = append(want, e)
							}
						}
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("after=%q filter=%q limit=%d: got %+v; want %+v", after, filter, limit, got, want)
						}
					}
				}
			}
		})
	}
}

func TestRuntimeKVListPrefixRangeQueryPlan(t *testing.T) {
	conn, _ := runtimeKVListFixture(t)
	for _, tc := range []struct{ prefix, after, lower string }{
		{"", "", "key>''"},
		{"", "z", "key>'z'"},
		{"missing", "", "key>='missing'"},
		{"é", "a", "key>='é'"},
		{"é", "é", "key>'é'"},
		{"é", "éa", "key>'éa'"},
		{"é", "中", "key>'中'"},
		{"ÿ", "", "key>='ÿ'"},
		{"\U0010ffff", "", "key>='\U0010ffff'"},
	} {
		for _, filter := range []string{"", "alice"} {
			t.Run(fmt.Sprintf("%q/after=%q/filter=%q", tc.prefix, tc.after, filter), func(t *testing.T) {
				stmt, err := runtimeKVListStatement(conn, tc.prefix, tc.after, 100, filter)
				if err != nil {
					t.Fatal(err)
				}
				query := stmt.ExpandedSQL()
				stmt.Close()
				if !strings.Contains(query, "WHERE "+tc.lower) {
					t.Fatalf("effective lower bound missing: %s", query)
				}
				plan, _, err := conn.Prepare("EXPLAIN QUERY PLAN " + query)
				if err != nil {
					t.Fatal(err)
				}
				defer plan.Close()
				var detail string
				for plan.Step() {
					detail += plan.ColumnText(3) + "\n"
				}
				if err := plan.Err(); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(detail, "SEARCH kv USING PRIMARY KEY (key>?") || strings.Contains(detail, "SCAN") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatalf("expected ordered index range: %s", detail)
				}
				if tc.prefix != "" && !strings.Contains(detail, "AND key<?") {
					t.Fatalf("prefix upper bound missing: %s", detail)
				}
			})
		}
	}
}
