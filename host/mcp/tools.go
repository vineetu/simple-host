package mcp

import "encoding/json"

// Tool is one callable operation. run turns validated arguments into REST
// requests (through call.do) and shapes the answer; it holds no business rule
// of its own.
type Tool struct {
	Meta        map[string]any `json:"_meta,omitempty"`
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// OutputSchema describes structuredContent on a successful call (see
	// outputs.go). It is not sent to clients of 2025-03-26, which predates it.
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`

	policy toolPolicy
	run    func(c *call, args map[string]any) (output, error)
}

// MarshalJSON repeats the title inside annotations, where some clients and
// the connector directories read it, and opens OutputSchema's
// additionalProperties: false to true, recursively (openOutputSchemaForWire,
// outputs.go), so a client with an older cached copy of a tool's outputSchema
// never rejects a later, additive field as a failed call (SIM-384). This is
// the one place every Tool reaches the wire (tools/list and anywhere else
// that marshals one), so the fix applies everywhere a schema is advertised
// without touching the authored, closed schemas tests validate against.
func (t Tool) MarshalJSON() ([]byte, error) {
	type plain Tool
	if _, set := t.Annotations["title"]; t.Title != "" && !set {
		annotations := make(map[string]any, len(t.Annotations)+1)
		for k, v := range t.Annotations {
			annotations[k] = v
		}
		annotations["title"] = t.Title
		t.Annotations = annotations
	}
	if t.OutputSchema != nil {
		t.OutputSchema = openOutputSchemaForWire(t.OutputSchema)
	}
	return json.Marshal(plain(t))
}
