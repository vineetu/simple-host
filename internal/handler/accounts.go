package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
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
	if !visitorHandleRe.MatchString(prefix) {
		return nil, errors.New("prefix must contain 1 to 39 lowercase letters, digits or hyphens")
	}
	names := make([]string, *req.Count)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%02d", prefix, i+1)
	}
	return names, nil
}

func validateHandle(handle string) error {
	if !visitorHandleRe.MatchString(handle) || !handleIsLabel(handle) {
		return errors.New("handle must contain 1 to 39 lowercase letters, digits or hyphens (not at the start or end) and must not be reserved")
	}
	if labelReservedForNew(handle) {
		return errReservedName
	}
	return nil
}

// errReservedName: a new handle, claimed name or site name that is reserved
// (labelReservedForNew, reservedNewSiteNames).
var errReservedName = errors.New("this name is reserved; pick another")

// handleIsLabel: the handle is usable as a DNS label, since it is also the
// account's address (<handle>.<SITE_DOMAIN>).
func handleIsLabel(handle string) bool {
	return handle != "" && handle[0] != '-' && handle[len(handle)-1] != '-' && !strings.HasPrefix(handle, "xn--")
}

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
	created := make([]map[string]string, 0, len(names))
	skipped := make([]map[string]string, 0)
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	for _, name := range names {
		key, err := auth.GenerateAPIKey()
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO users (username) VALUES ($1) ON CONFLICT (username) DO NOTHING RETURNING id`, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			skipped = append(skipped, map[string]string{"username": name, "reason": "account already exists"})
			continue
		}
		if err == nil {
			err = db.AddAPIKey(r.Context(), tx, id, key, db.KeyNameEvent)
		}
		if err == nil {
			err = db.MarkEventAccount(r.Context(), tx, id)
		}
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		assignHandle(r.Context(), tx, id, name)
		user, err := db.GetUserByID(r.Context(), tx, id)
		if err != nil || !user.Handle.Valid {
			writeJSON(w, 500, errorResponse{Error: "could not assign account handle"})
			return
		}
		created = append(created, map[string]string{"username": name, "handle": user.Handle.String, "api_key": key, "display_name": user.DisplayName.String})
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"created": created, "skipped": skipped})
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	acct, err := db.LockAccountForDelete(r.Context(), tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, errorResponse{Error: "not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if acct.IsAdmin || acct.ID == h.adminUserID {
		writeJSON(w, 400, errorResponse{Error: "cannot delete an admin account"})
		return
	}
	if acct.EventClaims > 0 {
		writeJSON(w, 409, errorResponse{
			Error: "this account still holds event hostnames; release them first so their DNS records are removed", Code: "event_hostnames"})
		return
	}
	erased, err := db.EraseAccount(r.Context(), tx, acct)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		log.Printf("admin delete account %s: %v", acct.ID, err)
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if err = h.eraseAccountFiles(r.Context(), erased); err != nil {
		log.Printf("admin delete account %s: files: %v", acct.ID, err)
		writeJSON(w, 500, errorResponse{Error: "account removed but its files could not be deleted"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reissueAccountKey handles POST /v1/admin/users/{id}/key: the organiser's
// answer to "I lost my key". Every key the account holds is replaced by one
// new key, returned once. Sites, data and connected apps are untouched.
func (h *SiteHandler) reissueAccountKey(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	var id, username string
	var handle sql.NullString
	var admin, suspended bool
	err = tx.QueryRowContext(r.Context(), `SELECT id, username, handle, is_admin, suspended_at IS NOT NULL FROM users WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&id, &username, &handle, &admin, &suspended)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, errorResponse{Error: "not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if admin || id == h.adminUserID {
		writeJSON(w, 400, errorResponse{Error: "cannot reissue an admin account's key here"})
		return
	}
	// A suspended account gets no new key; re-enable it first.
	if suspended {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this account is suspended; re-enable it before giving it a new key", Code: "account_suspended"})
		return
	}
	key, err := auth.GenerateAPIKey()
	if err == nil {
		err = db.ReplaceAPIKeys(r.Context(), tx, id, key, db.KeyNameEvent)
	}
	if err == nil {
		err = db.MarkEventAccount(r.Context(), tx, id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	var oldHandle sql.NullString
	// First deploy takes this lock before reading the handle or creating files.
	err = tx.QueryRowContext(r.Context(), "SELECT handle FROM users WHERE id=$1 FOR UPDATE", user.ID).Scan(&oldHandle)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	changed := req.Handle != nil && *req.Handle != oldHandle.String
	renamed := false // moved with sites: the old handle stays as an alias
	if changed {
		// Sites in Recently deleted count: they come back at this account's
		// address, so their old links need the alias too.
		var hasSites bool
		err = tx.QueryRowContext(r.Context(), "SELECT EXISTS (SELECT 1 FROM sites WHERE user_id=$1)", user.ID).Scan(&hasSites)
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		// A handle that ever served files (its handles/<h> link exists, kept
		// after a rename or a purge) is kept as an alias too, even with no
		// sites left: old shared links and hand-made vhosts keyed on the
		// folder must never follow a stranger who claims it. Only a handle
		// that never published anything is freed.
		if oldHandle.String != "" && (hasSites || h.disk.HasHandleLink(oldHandle.String)) {
			// Owner decision 2026-09-27: the handle can change after
			// publishing; old links redirect through the alias. Once per
			// handleRenameEvery, so an address cannot be churned.
			limited, last, rerr := db.HandleRenamedSince(r.Context(), tx, user.ID, time.Now().Add(-handleRenameEvery()))
			if rerr != nil {
				writeJSON(w, 500, errorResponse{Error: "internal server error"})
				return
			}
			if limited {
				next := last.Add(handleRenameEvery()).UTC()
				w.Header().Set("Retry-After", strconv.Itoa(int(time.Until(next).Seconds())+1))
				writeJSON(w, http.StatusTooManyRequests, map[string]any{
					"error":             "you changed your address recently; you can change it again after " + next.Format("2 Jan 2006"),
					"next_change_after": next,
				})
				return
			}
			_, err = db.RenameHandleTx(r.Context(), tx, user.ID, *req.Handle)
			if errors.Is(err, db.ErrDomainTaken) || isUniqueViolation(err) {
				writeJSON(w, 409, errorResponse{Error: "handle already taken"})
				return
			}
			if err != nil {
				writeJSON(w, 500, errorResponse{Error: "internal server error"})
				return
			}
			renamed = true
		} else {
			free, nsErr := db.HandleAvailable(r.Context(), tx, user.ID, *req.Handle)
			if nsErr != nil {
				writeJSON(w, 500, errorResponse{Error: "internal server error"})
				return
			}
			if !free {
				writeJSON(w, 409, errorResponse{Error: "handle already taken"})
				return
			}
			_, err = tx.ExecContext(r.Context(), "UPDATE users SET handle=$2, handle_changed_at=now() WHERE id=$1", user.ID, *req.Handle)
			if isUniqueViolation(err) {
				writeJSON(w, 409, errorResponse{Error: "handle already taken"})
				return
			}
			if err != nil {
				writeJSON(w, 500, errorResponse{Error: "internal server error"})
				return
			}
			if err = h.disk.RemoveHandleLink(oldHandle.String); err != nil {
				writeJSON(w, 500, errorResponse{Error: "could not remove old handle link"})
				return
			}
		}
	}
	if req.DisplayName != nil {
		if _, err = tx.ExecContext(r.Context(), "UPDATE users SET display_name=NULLIF($2,'') WHERE id=$1", user.ID, *req.DisplayName); err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
	}
	if req.SignInAlerts != nil {
		if err = db.SetSignInAlerts(r.Context(), tx, user.ID, *req.SignInAlerts); err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
	}
	updated, err := db.GetUserByID(r.Context(), tx, user.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	alertsOn, err := db.SignInAlertsOn(r.Context(), tx, user.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if renamed {
		// The files answer under the new handle at once; the old handle's
		// link stays so old content-host paths keep resolving. The new
		// handle needs its own *.<handle> certificate: until it is ready,
		// every address handed out is the person-path one (siteHostLive).
		if err := h.disk.EnsureHandleLink(updated.Handle.String, updated.ID); err != nil {
			log.Printf("handle rename: ensure handle link %s: %v", updated.Handle.String, err)
		}
		h.RequestSiteCert(updated.Handle.String)
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
