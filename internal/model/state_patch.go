package model

import "encoding/json"

// StatePatch is one PATCH of the document: Before is the document as read
// (decoded), After the same with the ops applied, Next After as saved.
type StatePatch struct {
	Before any
	After  any
	Next   json.RawMessage
}
