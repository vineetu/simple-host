package web

import (
	"encoding/json"
)

func storageJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
