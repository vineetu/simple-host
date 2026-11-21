package siteshttp

import (
	"encoding/json"
)

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
