package storage

import (
	"encoding/json"
	"testing"
)

func TestStampInsertValues(t *testing.T) {
	cols := map[string]string{"visitor_id": "TEXT", "created_at": "TEXT", "updated_at": "TEXT", "approved_at": "TEXT"}
	values := map[string]json.RawMessage{"visitor_id": json.RawMessage(`"forged"`), "created_at": json.RawMessage(`"old"`), "updated_at": json.RawMessage(`"old"`), "approved_at": json.RawMessage(`"old"`)}
	stampInsertValues(cols, values, InsertActor{Hold: true, StampID: "visitor"})
	if string(values["visitor_id"]) != `"visitor"` || string(values["approved_at"]) != "null" || string(values["created_at"]) == `"old"` || values["updated_at"] != nil {
		t.Fatalf("visitor stamps: %s", values)
	}
	ownerValues := map[string]json.RawMessage{"approved_at": json.RawMessage(`"chosen"`)}
	stampInsertValues(cols, ownerValues, InsertActor{Owner: true, Hold: true})
	if string(ownerValues["approved_at"]) != `"chosen"` {
		t.Fatalf("owner value: %s", ownerValues)
	}
}
