package teams

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

func checkLine(w http.ResponseWriter, s, field string, min, max int) (string, bool) {
	value, e := events.Line(s, field, min, max)
	if e != nil {
		httpx.WriteError(w, 400, e.Code, e.Message)
		return "", false
	}
	return value, true
}
