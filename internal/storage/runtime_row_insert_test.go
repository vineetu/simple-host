package storage

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestInsertRowStatement(t *testing.T) {
	values := map[string]json.RawMessage{"z": []byte(`2`), `a"b`: []byte(`"x"`)}
	query, params := InsertRowStatement("items", "rowid", true, []string{"z", `a"b`}, values)
	if query != `INSERT OR ABORT INTO "items" ("a""b","z") VALUES (?,?) RETURNING rowid` || !reflect.DeepEqual(params, []json.RawMessage{values[`a"b`], values["z"]}) {
		t.Fatalf("unexpected statement %q, %s", query, params)
	}
	query, params = InsertRowStatement("items", "id", false, nil, nil)
	if query != `INSERT INTO "items" DEFAULT VALUES RETURNING id` || len(params) != 0 {
		t.Fatalf("unexpected default statement %q, %s", query, params)
	}
}
