package operator

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/ops"
)

// maxAccountsBody bounds the request body, not the number of accounts. An
// emails array is the only unbounded thing a caller sends here, and 8 MB holds
// something like two hundred thousand addresses — far past any real event. It
// exists so a malformed body cannot be read into memory without limit, and for
// no other reason.
const maxAccountsBody = 8 << 20

type bulkUsersRequest struct {
	Emails *[]string `json:"emails"`
	Count  *int      `json:"count"`
	Prefix *string   `json:"prefix"`
}

func bulkUsernames(req bulkUsersRequest) ([]string, error) {
	if (req.Emails == nil) == (req.Count == nil) || (req.Emails != nil && req.Prefix != nil) {
		return nil, errors.New("provide exactly one of emails or count with optional prefix")
	}
	if req.Emails != nil {
		if len(*req.Emails) == 0 {
			return nil, errors.New("emails must contain at least one address")
		}
		names := make([]string, len(*req.Emails))
		for i, email := range *req.Emails {
			norm, ok := authrules.NormalEmail(email)
			if !ok {
				return nil, fmt.Errorf("invalid email: %s", strings.ToLower(strings.TrimSpace(email)))
			}
			names[i] = norm
		}
		return names, nil
	}
	if *req.Count < 1 {
		return nil, errors.New("count must be at least 1")
	}
	prefix := "guest"
	if req.Prefix != nil {
		prefix = strings.TrimSpace(*req.Prefix)
	}
	return BulkAccountNames(prefix, *req.Count)
}

func (h *Handler) createAccounts(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	var req bulkUsersRequest
	if !httpx.DecodeJSON(w, r, maxAccountsBody, &req) {
		return
	}
	// No ceiling, and nothing here refuses a batch on a projection. What an
	// instance can hold is whatever fits, and what fits is reported by
	// /v1/admin/usage from the real disk rather than guessed in advance.
	names, err := bulkUsernames(req)
	if err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	out, err := CreateAccounts(r.Context(), h.Database, names, h.Ed.Identity().RandomHandles, accounts.AssignHandle)
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// reissueAccountKey handles POST /v1/admin/users/{id}/key: the organiser's
// answer to "I lost my key". Every key the account holds is replaced by one
// new key, returned once. Sites, data and connected apps are untouched.
func (h *Handler) reissueAccountKey(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	out, err := ReissueAccountKey(r.Context(), h.Database, r.PathValue("id"))
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	id, username, handle, key := out.User.ID, out.User.Username, out.User.Handle, out.Key

	log.Printf("admin_key_reissue user_id=%s by=%s", id, auth.GetUser(r.Context()).ID)
	httpx.WriteJSON(w, http.StatusOK, map[string]string{
		"id": id, "username": username, "handle": handle.String, "api_key": key,
		"message": "Every earlier key for this account stopped working. Hand this one over now; it is not shown again.",
	})
}

// Handler serves the operator's account routes: make accounts, reissue an
// account's key, and the sign-ups report (signups.go).
type Handler struct {
	Database  *sql.DB
	Ed        *edition.Edition
	Reporting *ops.Reporting
}

// Register mounts the routes; siteOpLimiter is the site routes' own. The
// limiter sits outside auth on the report so wrong keys typed at /admin
// count too.
func (h *Handler) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler, siteOpLimiter *httpx.RateLimiter) {
	mux.Handle("POST /v1/admin/users", authMiddleware(http.HandlerFunc(h.createAccounts)))
	mux.Handle("POST /v1/admin/users/{id}/key", authMiddleware(http.HandlerFunc(h.reissueAccountKey)))
	mux.Handle("GET /v1/admin/signups", httpx.RateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.adminSignups))))
	mux.Handle("GET /v1/admin/signups.csv", httpx.RateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.adminSignupsCSV))))
}
