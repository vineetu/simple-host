package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// DecodeJSON is the one request-body reader: at most max bytes of JSON into
// dst. A missing or malformed body answers 400 invalid_request, an oversized
// one 413 item_too_large; either way it returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, max int64, dst any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, max)).Decode(dst)
	var tooLarge *http.MaxBytesError
	switch {
	case err == nil:
		return true
	case errors.As(err, &tooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, "item_too_large", "request body is too large ("+strconv.FormatInt(max, 10)+" bytes at most)")
	case errors.Is(err, io.EOF):
		WriteError(w, http.StatusBadRequest, "invalid_request", "JSON body required")
	default:
		WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body")
	}
	return false
}

// Limit is the one page-size reader: ?limit= from 1 up, capped at max, def
// when absent; false when it is not a whole number of at least 1.
func Limit(q url.Values, def, max int) (int, bool) {
	v := q.Get("limit")
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	return min(n, max), err == nil && n >= 1
}
