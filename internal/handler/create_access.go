package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

type createAccessKey struct{}

// Creation access is validated before either upload path can write anything.
func (h *SiteHandler) readCreateAccess(w http.ResponseWriter, r *http.Request, access *string, viewers []string) bool {
	if r.Method == http.MethodPut && r.URL.Query().Get("create") != "1" && r.URL.Query().Get("create") != "true" && (access != nil || viewers != nil) {
		writeJSON(w, 400, errorResponse{Code: "invalid_request", Error: "access is chosen at creation; later use set_site_access / grant_site_viewer"})
		return false
	}
	in := sites.CreateInput{}
	if access != nil {
		in.Access = *access
		if in.Access != accessAnyone && in.Access != accessSpecific {
			writeJSON(w, 400, errorResponse{Code: "invalid_request", Error: "access must be anyone or specific"})
			return false
		}
	}
	if viewers != nil && in.Access != accessSpecific {
		writeJSON(w, 400, errorResponse{Code: "invalid_request", Error: "viewers requires access specific"})
		return false
	}
	seen := map[string]bool{}
	for _, raw := range viewers {
		email, ok := normalizeViewerEmail(raw)
		if !ok {
			writeJSON(w, 400, errorResponse{Code: "invalid_email", Error: "not an email address: " + strings.TrimSpace(raw)})
			return false
		}
		if !seen[email] {
			seen[email] = true
			in.Viewers = append(in.Viewers, email)
		}
	}
	if len(in.Viewers) > h.limits.SiteViewersMax {
		writeJSON(w, 400, errorResponse{Code: "too_many_viewers", Error: "a site can name at most " + strconv.Itoa(h.limits.SiteViewersMax) + " viewers"})
		return false
	}
	in.NamedViewersRefusal = func(handle, name string) (string, string) {
		return h.namedViewersRefusal(model.Site{OwnerHandle: handle, Name: name}, model.SitePasscodeRow{})
	}
	*r = *r.WithContext(context.WithValue(r.Context(), createAccessKey{}, in))
	return true
}

func (h *SiteHandler) readArchiveCreateAccess(w http.ResponseWriter, r *http.Request) bool {
	// URL.Query drops pairs it cannot parse (a ";" separator, a bad
	// escape). A query that names access or viewers must parse cleanly, or
	// a private request could silently become an open site.
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil && queryNamesCreateAccess(r.URL.RawQuery) {
		writeJSON(w, 400, errorResponse{Code: "invalid_request", Error: "the query string could not be read; send access and viewers as ?access=specific&viewers=a@example.com,b@example.com"})
		return false
	}
	var access *string
	if q.Has("access") {
		v := q.Get("access")
		access = &v
	}
	var viewers []string
	if q.Has("viewers") {
		viewers = []string{}
		for _, v := range q["viewers"] {
			viewers = append(viewers, strings.Split(v, ",")...)
		}
	}
	return h.readCreateAccess(w, r, access, viewers)
}

func creationAccess(r *http.Request) sites.CreateInput {
	in, _ := r.Context().Value(createAccessKey{}).(sites.CreateInput)
	return in
}

func (in *filesRequest) UnmarshalJSON(data []byte) error {
	type body filesRequest
	var decoded body
	if err := json.Unmarshal(data, &decoded); err != nil {
		var fieldErr *json.UnmarshalTypeError
		if errors.As(err, &fieldErr) && (fieldErr.Field == "access" || fieldErr.Field == "viewers") {
			return &sites.Error{Code: "invalid_request", Message: "access must be anyone or specific; viewers must be an array of emails"}
		}
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// encoding/json matches field names without regard to case, so presence
	// does too: {"Access": null} is a bad access value, never "omitted".
	for key := range fields {
		switch {
		case strings.EqualFold(key, "access") && decoded.Access == nil:
			v := ""
			decoded.Access = &v
		case strings.EqualFold(key, "viewers") && decoded.Viewers == nil:
			decoded.Viewers = []string{}
		}
	}
	*in = filesRequest(decoded)
	return nil
}

// queryNamesCreateAccess reports whether any pair of a raw query might name
// access or viewers: each key is decoded on its own (a bad value elsewhere
// cannot hide it), and a key that does not decode counts as naming them.
func queryNamesCreateAccess(raw string) bool {
	for _, pair := range strings.FieldsFunc(raw, func(c rune) bool { return c == '&' || c == ';' }) {
		key, _, _ := strings.Cut(pair, "=")
		decoded, err := url.QueryUnescape(key)
		if err != nil {
			return true
		}
		decoded = strings.ToLower(decoded)
		if strings.Contains(decoded, "access") || strings.Contains(decoded, "viewers") {
			return true
		}
	}
	return false
}
