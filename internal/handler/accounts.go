package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	appsites "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
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

// requestedCount reports how many accounts a request asks for, validating its
// shape but generating nothing. The capacity check runs on this number before
// bulkUsernames allocates a slice the size of the answer.
func requestedCount(req bulkUsersRequest) (int, error) {
	if (req.Emails == nil) == (req.Count == nil) || (req.Emails != nil && req.Prefix != nil) {
		return 0, errors.New("provide exactly one of emails or count with optional prefix")
	}
	if req.Emails != nil {
		return len(*req.Emails), nil
	}
	return *req.Count, nil
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
			email = strings.ToLower(strings.TrimSpace(email))
			if !validEmail.MatchString(email) {
				return nil, fmt.Errorf("invalid email: %s", email)
			}
			names[i] = email
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
	return appsites.BulkAccountNames(prefix, *req.Count)
}

func validateHandle(handle string) error { return appsites.CheckNewHandle(handle) }

// errReservedName: a new handle, claimed name or site name that is reserved
// (the new-claim policy in app/sites).
var errReservedName = appsites.ErrReservedHandle

func normalizeDisplayName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 100 {
		return "", errors.New("display_name must be at most 100 characters")
	}
	return name, nil
}

func accountAdmin(w http.ResponseWriter, r *http.Request) bool {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return false
	}
	if !user.IsAdmin {
		// 404, not 403: a signed-in non-admin should not learn this endpoint
		// exists, and the /admin page renders whatever this returns as a plain
		// not-found. Same reasoning as the reserved-handle 404 on the page route.
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return false
	}
	return true
}

func (h *SiteHandler) createAccounts(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAccountsBody)
	var req bulkUsersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	// No ceiling, and nothing here refuses a batch on a projection. What an
	// instance can hold is whatever fits, and what fits is reported by
	// /v1/admin/usage from the real disk rather than guessed in advance.
	names, err := bulkUsernames(req)
	if err != nil {
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return
	}
	out, err := h.storeAccounts().CreateAccounts(r.Context(), names, h.ed.Identity().RandomHandles, func(ctx context.Context, q accounts.AttributionStore, id, name string, random bool) {
		assignHandle(ctx, q, id, name, random)
	})
	if writeLifecycleFailure(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// deleteAccount handles DELETE /v1/admin/users/{id}: the same immediate,
// final erasure as DELETE /v1/me (account_data.go), started by the operator.
// A suspended account can be deleted here; an admin account cannot.
func (h *SiteHandler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	// Account ids are lowercase uuids (users.id::text); the site locks are
	// keyed by that exact string, so take them before the row, as DELETE /v1/me does.
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	unlock, err := h.lockAccountSites(r.Context(), id)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer unlock()
	deleted, err := h.storeAccounts().DeleteAccount(r.Context(), accounts.DeleteAccountInput{ID: id, AdminID: h.adminUserID, Operator: true, HoldingAccounts: h.ed.Identity().HoldingAccounts, Teams: members.AccountErasure{}})
	if writeLifecycleFailure(w, err) {
		return
	}
	erased := deleted.Erased

	if err = h.eraseAccountFiles(r.Context(), erased); err != nil {
		log.Printf("admin delete account %s: files: %v", id, err)
		writeJSON(w, 500, errorResponse{Error: "account removed but its files could not be deleted"})
		return
	}
	h.sweepOrphanTeamSites(r.Context(), erased.AffectedEventAccounts)
	w.WriteHeader(http.StatusNoContent)
}

// reissueAccountKey handles POST /v1/admin/users/{id}/key: the organiser's
// answer to "I lost my key". Every key the account holds is replaced by one
// new key, returned once. Sites, data and connected apps are untouched.
func (h *SiteHandler) reissueAccountKey(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	out, err := h.storeAccounts().ReissueAccountKey(r.Context(), r.PathValue("id"), h.adminUserID, h.ed.Identity().HoldingAccounts)
	if writeLifecycleFailure(w, err) {
		return
	}
	id, username, handle, key := out.User.ID, out.User.Username, out.User.Handle, out.Key

	log.Printf("admin_key_reissue user_id=%s by=%s", id, auth.GetUser(r.Context()).ID)
	writeJSON(w, http.StatusOK, map[string]string{
		"id": id, "username": username, "handle": handle.String, "api_key": key,
		"message": "Every earlier key for this account stopped working. Hand this one over now; it is not shown again.",
	})
}

func (h *SiteHandler) patchMe(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, 401, errorResponse{Error: "unauthorized"})
		return
	}
	var req profileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	if h.ed.Identity().RandomHandles && req.Handle != nil && *req.Handle != user.Handle.String {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "Simple Hack accounts have no address to change", Code: "handle_fixed"})
		return
	}
	// A new address is judged first, so a taken, reserved or malformed one
	// is refused at once with the address named (JudgeHandle).
	if req.Handle != nil && *req.Handle != user.Handle.String {
		v, jerr := JudgeHandle(r.Context(), h.database, user.ID, *req.Handle)
		if jerr != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		if v.Status != 0 {
			writeJSON(w, v.Status, errorResponse{Error: v.Error, Code: v.Code, Address: v.Address})
			return
		}
	}
	if err := validateProfile(&req); err != nil {
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return
	}
	if req.SignInAlerts != nil && user.KeyHash == "" {
		// A connected app (or a stolen connection) must not be able to
		// silence the email that would reveal it.
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "turning sign-in alerts on or off needs one of the account's own API keys (X-API-Key), not the admin key or a connected app", Code: "not_an_account_key"})
		return
	}
	updated, alertsOn, err := h.sitesSvc().UpdateProfile(r.Context(), appsites.ProfileInput{UserID: user.ID, DisplayName: req.DisplayName, Handle: req.Handle, SignInAlerts: req.SignInAlerts, RenameEvery: h.handleRenameEvery()})
	if err != nil {
		var failure *accounts.LifecycleError
		if errors.As(err, &failure) {
			if failure.Code == "rate_limited" {
				w.Header().Set("Retry-After", strconv.Itoa(int(time.Until(failure.Next).Seconds())+1))
				writeJSON(w, 429, map[string]any{"code": "rate_limited", "error": failure.Message, "next_change_after": failure.Next})
				return
			}
			if failure.Code == "handle_taken" {
				writeJSON(w, 409, errorResponse{Error: handleTakenMsg(*req.Handle), Code: "handle_taken", Address: handleAddress(*req.Handle)})
				return
			}
		}
		writeLifecycleFailure(w, err)
		return
	}

	writeJSON(w, 200, meResponse{ID: updated.ID, Username: updated.Username, IsAdmin: updated.IsAdmin, Handle: updated.Handle.String, DisplayName: updated.DisplayName.String, PublicPage: h.PersonPageURL(updated.Handle.String), SignInAlerts: &alertsOn})
}

type profileRequest struct {
	DisplayName  *string `json:"display_name"`
	Handle       *string `json:"handle"`
	SignInAlerts *bool   `json:"signin_alerts"`
}

func validateProfile(req *profileRequest) error {
	if req.DisplayName == nil && req.Handle == nil && req.SignInAlerts == nil {
		return errors.New("provide display_name, handle and/or signin_alerts")
	}
	if req.DisplayName != nil {
		name, err := normalizeDisplayName(*req.DisplayName)
		if err != nil {
			return err
		}
		*req.DisplayName = name
	}
	if req.Handle != nil {
		return validateHandle(*req.Handle)
	}
	return nil
}
