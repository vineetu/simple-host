package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// maxBulkAccounts caps one create-accounts call. The number is a guard against
// a typo turning into a million rows and an hour-long transaction, not a
// product limit: what an instance can really hold is set by its disk, which
// internal/capacity works out and the setup page asks about.
const maxBulkAccounts = 5000

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
		if len(*req.Emails) == 0 || len(*req.Emails) > maxBulkAccounts {
			return nil, fmt.Errorf("emails must contain 1 to %d accounts", maxBulkAccounts)
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
	if *req.Count < 1 || *req.Count > maxBulkAccounts {
		return nil, fmt.Errorf("count must be between 1 and %d accounts", maxBulkAccounts)
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
	if !visitorHandleRe.MatchString(handle) || reservedHandles[handle] {
		return errors.New("handle must contain 1 to 39 lowercase letters, digits or hyphens and must not be reserved")
	}
	return nil
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
	var req bulkUsersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
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
		err = tx.QueryRowContext(r.Context(), `INSERT INTO users (username, api_key) VALUES ($1,$2) ON CONFLICT (username) DO NOTHING RETURNING id`, name, key).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			skipped = append(skipped, map[string]string{"username": name, "reason": "account already exists"})
			continue
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

func (h *SiteHandler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	var id string
	var handle sql.NullString
	var admin bool
	err = tx.QueryRowContext(r.Context(), `SELECT id, handle, is_admin FROM users WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&id, &handle, &admin)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, errorResponse{Error: "not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if admin || id == h.adminUserID {
		writeJSON(w, 400, errorResponse{Error: "cannot delete an admin account"})
		return
	}
	// event_domains cascades on user_id, so deleting the row would take the
	// claim with it and strand live DNS records under our domain that nothing
	// could then find or remove. Make the operator release them first.
	var claims int
	if err = tx.QueryRowContext(r.Context(),
		`SELECT count(*) FROM event_domains WHERE user_id=$1`, id).Scan(&claims); err == nil && claims > 0 {
		writeJSON(w, 409, errorResponse{
			Error: "this account still holds event hostnames; release them first so their DNS records are removed"})
		return
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM users WHERE id=$1", id); err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	// Disk comes after the commit, never before. The other order means a commit
	// failure rolls the row back while the files are already gone, leaving a
	// live account with a working key whose content has been destroyed. This
	// way a failure here strands a directory an operator can delete.
	if err = h.disk.DeleteUser(id, handle.String); err != nil {
		writeJSON(w, 500, errorResponse{Error: "account removed but its files could not be deleted"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
	if changed {
		var hasSites bool
		err = tx.QueryRowContext(r.Context(), "SELECT EXISTS (SELECT 1 FROM sites WHERE user_id=$1)", user.ID).Scan(&hasSites)
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		if hasSites {
			writeJSON(w, 409, errorResponse{Error: "the address is fixed once something is published"})
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
	if req.DisplayName != nil {
		if _, err = tx.ExecContext(r.Context(), "UPDATE users SET display_name=NULLIF($2,'') WHERE id=$1", user.ID, *req.DisplayName); err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
	}
	updated, err := db.GetUserByID(r.Context(), tx, user.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if err = tx.Commit(); err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, 200, meResponse{ID: updated.ID, Username: updated.Username, IsAdmin: updated.IsAdmin, Handle: updated.Handle.String, DisplayName: updated.DisplayName.String})
}

type profileRequest struct {
	DisplayName *string `json:"display_name"`
	Handle      *string `json:"handle"`
}

func validateProfile(req *profileRequest) error {
	if req.DisplayName == nil && req.Handle == nil {
		return errors.New("provide display_name and/or handle")
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
