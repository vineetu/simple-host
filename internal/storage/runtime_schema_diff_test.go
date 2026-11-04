package storage

import (
	"reflect"
	"testing"
)

func TestSchemaTableChanges(t *testing.T) {
	before := map[string]bool{"old": true, "kept": true}
	after := map[string]bool{"kept": true, "z": true, "a": true}
	renamed, added := SchemaTableChanges(before, after, func(table string) bool { return table == "old" })
	if !renamed || !reflect.DeepEqual(added, []string{"a", "z"}) {
		t.Fatalf("got renamed=%v, added=%v", renamed, added)
	}
}
