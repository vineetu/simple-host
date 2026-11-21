package siteshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

type createAccessKey struct{}

func (h *SiteHandler) readCreateAccess(w http.ResponseWriter, r *http.Request, access *string, viewers []string) bool {
	if r.Method == http.MethodPut && r.URL.Query().Get("create") != "1" && r.URL.Query().Get("create") != "true" && (access != nil || viewers != nil) {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Code: "invalid_request", Error: "access is chosen at creation; later use set_site_access / grant_site_viewer"})
		return false
	}
	in := sites.CreateInput{}
	if access != nil {
		in.Access = *access
		if in.Access != web.AccessAnyone && in.Access != web.AccessSpecific {
			httpx.WriteJSON(w, 400, httpx.ErrorResponse{Code: "invalid_request", Error: "access must be anyone or specific"})
			return false
		}
	}
	if viewers != nil && in.Access != web.AccessSpecific {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Code: "invalid_request", Error: "viewers requires access specific"})
		return false
	}
	emails, err := accounts.ViewerEmails(viewers, h.Limits.SiteViewersMax)
	if err != nil {
		web.WriteAccessError(w, err)
		return false
	}
	in.Viewers = append(in.Viewers, emails...)
	in.NamedViewersRefusal = func(handle, name string) (string, string) {
		site := model.Site{OwnerHandle: handle, Name: name}
		var refusal *accounts.Refusal
		if errors.As(accounts.NamedViewersRefusal(model.SitePasscodeRow{}, h.Hosts.GateSite(site, h.noVisitorSignIn)), &refusal) {
			return refusal.Code, refusal.Message
		}
		return "", ""
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
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Code: "invalid_request", Error: "the query string could not be read; send access and viewers as ?access=specific&viewers=a@example.com,b@example.com"})
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
