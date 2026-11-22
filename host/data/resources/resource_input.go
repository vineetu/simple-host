package resources

import (
	"encoding/json"
	"strings"

	"github.com/vsriram/simple-host/host/data"
)

// presetFields are the fields of the removed presets; a resource body that
// names any of them, even empty or null, is refused (invalid_resource).
var presetFields = []string{"preset", "read", "add", "edit", "delete", "write", "write_mode", "approve_first"}

// DecodeResourceInput reads a resource body (PUT .../storage/resources/{name}):
// kind, rules, site_passcode and, for SQLite, per-table rules. Call and Name
// are the caller's to fill in.
func DecodeResourceInput(body []byte) (ResourceInput, error) {
	var req struct {
		Kind         string                      `json:"kind"`
		Rules        *json.RawMessage            `json:"rules"`
		SitePasscode string                      `json:"site_passcode"`
		Tables       *map[string]json.RawMessage `json:"tables"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ResourceInput{}, invalidResourceBody()
	}
	in := ResourceInput{Kind: req.Kind, Rules: req.Rules, SitePasscode: req.SitePasscode, PresetFieldsPresent: presetFieldsPresent(body)}
	if req.Tables == nil {
		return in, nil
	}
	tables := map[string]ResourceTableInput{}
	for name, raw := range *req.Tables {
		var table struct {
			Rules *json.RawMessage `json:"rules"`
		}
		if json.Unmarshal(raw, &table) != nil {
			return ResourceInput{}, invalidResourceBody()
		}
		tables[name] = ResourceTableInput{Rules: table.Rules, PresetFieldsPresent: presetFieldsPresent(raw)}
	}
	in.Tables = &tables
	return in, nil
}

func invalidResourceBody() error {
	return &data.Error{Code: "invalid_request", Message: "invalid JSON body"}
}

func presetFieldsPresent(body []byte) bool {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(body, &fields)
	for key := range fields {
		for _, name := range presetFields {
			if strings.EqualFold(key, name) {
				return true
			}
		}
	}
	return false
}
