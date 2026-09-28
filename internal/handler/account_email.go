package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Change sign-in email: POST /v1/me/email sends a code to the new address AND
// a code to the current one; POST /v1/me/email/verify with both codes moves
// the account there. Only the person's own key: a connected app must not be
// able to move the account out from under its owner, and a key alone is not
// enough either (both inboxes must agree).
//
// On a change, every other API key of the account is revoked (the one that
// made the change stays), emailed codes and idle-cleanup links for the old
// address end, and the old address is told, with a one-click undo link valid
// for 7 days (GET /v1/me/email/undo shows a confirmation page, its button
// POSTs). Google/GitHub sign-ins stay linked; the owner app lists them with
// Unlink (GET /v1/me/identities, DELETE /v1/me/identities/{id}).
//
// Refused for the plugin reviewer account (and moving any account TO its
// address), admin, event and preview accounts, and accounts whose sign-in
// name is not an email address.
//
// "Another account already uses that address" is said only after the codes
// are verified, i.e. to someone who reads that inbox, which is what sign-in
// itself reveals (it answers created: false). Asking for a code looks the same
// for every address.
//
// Private-list entries keep the address they were stamped with; nothing is
// rewritten after a change.

type emailChangeRequest struct {
	Email string `json:"email"`
}

type emailChangeVerifyRequest struct {
	Code        string `json:"code"`         // sent to the new address
	CurrentCode string `json:"current_code"` // sent to the current address
}

func hashEmailChangeCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// SetPreviewAccounts names the PREVIEW_ACCOUNTS (their sites expire); they
// cannot change their sign-in email (it would take them off the list).
func (h *UserHandler) SetPreviewAccounts(accounts map[string]bool) { h.previewAccounts = accounts }

// emailChangeRefused writes why the account cannot change its sign-in email
// and reports true, or reports false.
func (h *UserHandler) emailChangeRefused(w http.ResponseWriter, r *http.Request, user *db.User) bool {
	refuse := func(code, msg string) bool {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: msg, Code: code})
		return true
	}
	switch {
	case h.reviewerEmail != "" && strings.EqualFold(user.Username, h.reviewerEmail):
		return refuse("reviewer_account", "this account's email is set by the operator")
	case user.IsAdmin || user.Username == "admin":
		return refuse("admin_account", "the admin account's sign-in email cannot be changed here")
	case h.previewAccounts[strings.ToLower(user.Username)]:
		return refuse("preview_account", "this account's email is set by the operator")
	case !strings.Contains(user.Username, "@"):
		return refuse("no_current_email", supportText("this account does not sign in with an email address, so there is none to confirm the change from; write to support@simple-host.app"))
	}
	t, err := db.GetSignInAlertTarget(r.Context(), h.database, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return true
	}
	if t.Event {
		return refuse("event_account", "an event account's name is set by its organiser and cannot be changed")
	}
	return false
}

func (h *UserHandler) requestEmailChange(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "changing the sign-in email")
	if user == nil {
		return
	}
	if h.emailChangeRefused(w, r, user) {
		return
	}
	var req emailChangeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	address := strings.TrimSpace(strings.ToLower(req.Email))
	if address == "" || !validEmail.MatchString(address) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "valid email is required", Code: "invalid_email"})
		return
	}
	current := user.Username
	if address == strings.ToLower(current) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "that is already your sign-in email", Code: "same_email"})
		return
	}
	if h.reviewerEmail != "" && strings.EqualFold(address, h.reviewerEmail) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "that address cannot be used to sign in", Code: "reserved_email"})
		return
	}
	// The same throttles as sign-in: per address (no mail-bombing either
	// inbox) and per account.
	if !h.emailLimiter.allow(emailLimiterKey(address)) || !h.emailLimiter.allow(emailLimiterKey(current)) || !h.emailLimiter.allow("email-change:"+user.ID) {
		writeEmailCodeError(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down"})
		return
	}
	code, err := generateNumericCode(6)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	oldCode, err := generateNumericCode(6)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := db.PutEmailChange(r.Context(), h.database, user.ID, address, hashEmailChangeCode(code), hashEmailChangeCode(oldCode), time.Now().Add(authTokenTTL())); err != nil {
		log.Printf("email change user_id=%s: store: %v", user.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := h.sendEmailChangeCode(address, code); err != nil {
		log.Printf("email change: send code to %s: %v", redactEmail(address), err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not send verification email"})
		return
	}
	if err := h.sendEmailChangeCurrentCode(current, address, oldCode); err != nil {
		log.Printf("email change: send code to current %s: %v", redactEmail(current), err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not send verification email"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"message":            "Check " + address + " and " + current + " for a 6-digit code each, then send both to POST /v1/me/email/verify as code (from the new address) and current_code (from the current one).",
		"email":              address,
		"current_email":      current,
		"expires_in_seconds": int(authTokenTTL().Seconds()),
	})
}

func (h *UserHandler) sendEmailChangeCode(address, code string) error {
	mailer, ok := h.mailer.(noticeSender)
	if !ok {
		return h.mailer.SendSignInCode(address, code, "")
	}
	return mailer.SendNotice(address, "Simple Host confirmation code: "+code, `Someone asked to make this address the sign-in email of a Simple Host account.

Your confirmation code:

    `+code+`

It expires in `+config.Span(authTokenTTL())+`. If you didn't ask for this, ignore this email: nothing changes.

Simple Host
`)
}

// sendEmailChangeCurrentCode sends the code the current address must give
// for the change to go through.
func (h *UserHandler) sendEmailChangeCurrentCode(current, newAddress, code string) error {
	mailer, ok := h.mailer.(noticeSender)
	if !ok {
		return h.mailer.SendSignInCode(current, code, "")
	}
	return mailer.SendNotice(current, "Simple Host: confirm changing your sign-in email ("+code+")", supportText(`Someone signed in to your Simple Host account asked to change its sign-in email to `+maskEmail(newAddress)+`.

If that was you, enter this code together with the one sent to the new address:

    `+code+`

It expires in `+config.Span(authTokenTTL())+`. If it wasn't you, do not share this code: without it nothing changes. Someone has one of your keys, so sign in and remove the keys you don't recognise, or write to support@simple-host.app.

Simple Host
`))
}

func (h *UserHandler) verifyEmailChange(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "changing the sign-in email")
	if user == nil {
		return
	}
	if h.emailChangeRefused(w, r, user) {
		return
	}
	var req emailChangeVerifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	code := strings.ReplaceAll(strings.TrimSpace(req.Code), "-", "")
	oldCode := strings.ReplaceAll(strings.TrimSpace(req.CurrentCode), "-", "")
	if code == "" || oldCode == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "code (sent to the new address) and current_code (sent to your current address) are both required", Code: "codes_required"})
		return
	}
	ctx := r.Context()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	var oldEmail string
	if err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1 FOR UPDATE`, user.ID).Scan(&oldEmail); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	change, err := db.GetEmailChangeForUpdate(ctx, tx, user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "no email change is waiting, or its code expired; ask for a new code", Code: "no_pending_change"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if change.Attempts >= maxCodeAttempts {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "too many attempts, request a new code", Code: "invalid_code"})
		return
	}
	if !h.emailLimiter.allow(emailLimiterKey(change.NewEmail)) {
		writeEmailCodeError(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down"})
		return
	}
	newOK := subtle.ConstantTimeCompare([]byte(hashEmailChangeCode(code)), []byte(change.CodeHash)) == 1
	oldOK := change.OldCodeHash != "" && subtle.ConstantTimeCompare([]byte(hashEmailChangeCode(oldCode)), []byte(change.OldCodeHash)) == 1
	if !newOK || !oldOK {
		if err := db.CountEmailChangeAttempt(ctx, tx, user.ID); err == nil {
			_ = tx.Commit()
		}
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid or expired code", Code: "invalid_code"})
		return
	}
	if h.reviewerEmail != "" && strings.EqualFold(change.NewEmail, h.reviewerEmail) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "that address cannot be used to sign in", Code: "reserved_email"})
		return
	}
	taken, err := db.EmailInUseByOther(ctx, tx, user.ID, change.NewEmail)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if taken {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "another Simple Host account already signs in with that address", Code: "email_taken"})
		return
	}
	undo, err := randomHex(24)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	err = db.ApplyEmailChange(ctx, tx, user.ID, oldEmail, change.NewEmail, user.KeyHash, hashEmailChangeCode(undo))
	if err == nil {
		err = tx.Commit()
	}
	if isUniqueViolation(err) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "another Simple Host account already signs in with that address", Code: "email_taken"})
		return
	}
	if err != nil {
		log.Printf("email change user_id=%s: apply: %v", user.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("email change user_id=%s: sign-in email changed", user.ID)
	h.emailOldAddress(oldEmail, change.NewEmail, undo)
	writeJSON(w, http.StatusOK, map[string]string{
		"username": change.NewEmail,
		"email":    change.NewEmail,
		"message":  "Your sign-in email is now " + change.NewEmail + ". Sign in with it from now on. Your other keys were signed out; this one still works. A notice went to your old address.",
	})
}

func (h *UserHandler) undoLink(tok string) string {
	return strings.TrimRight(h.publicBaseURL, "/") + "/v1/me/email/undo#t=" + tok
}

// emailOldAddress tells the old address where the account went, masked,
// with the undo link.
func (h *UserHandler) emailOldAddress(oldEmail, newEmail, undoToken string) {
	mailer, ok := h.mailer.(noticeSender)
	if !ok || !strings.Contains(oldEmail, "@") {
		return
	}
	text := "Your Simple Host sign-in email was changed to " + maskEmail(newEmail) + `. From now on you sign in with that address.

If this wasn't you, undo it (opens a page with an Undo button; works for ` + config.Span(db.EmailChangeUndoTTL()) + `):
` + h.undoLink(undoToken) + `

Undoing puts the account back on this address, signs out every key and every sign-in on sites, and removes Google or GitHub sign-ins and connected apps added since the change. Questions: support@simple-host.app.

Simple Host
`
	h.alerts.run(func() {
		if err := mailer.SendNotice(oldEmail, "Your Simple Host sign-in email was changed", supportText(text)); err != nil {
			log.Printf("email change: notice to old address: %v", err)
		}
	})
}

// undoEmailChange GET /v1/me/email/undo?t= shows the Undo button; POST (the
// button, t in the form) puts the account back on the old address.
func (h *UserHandler) undoEmailChange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if fragmentLinkGET(w, r, "", "/v1/me/email/undo") {
		return
	}
	tok := linkToken(w, r)
	home := strings.TrimRight(h.publicBaseURL, "/") + "/"
	gone := func() {
		writeMessagePage(w, r, "", http.StatusNotFound, "This link has already been used or has expired",
			supportText("If your sign-in email was changed without you, write to support@simple-host.app."), home, "Go to Simple Host", "")
	}
	if len(tok) != 48 {
		gone()
		return
	}
	hash := hashEmailChangeCode(tok)
	if linkConfirming(r) {
		u, err := db.GetEmailChangeUndo(r.Context(), h.database, hash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				gone()
				return
			}
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		writeMessagePage(w, r, "", http.StatusOK, "Undo the sign-in email change?",
			"The account goes back to signing in with "+html.EscapeString(u.OldEmail)+". Every key and every sign-in on sites is signed out, and Google or GitHub sign-ins and connected apps added since the change are removed.",
			"", "", confirmForm("/v1/me/email/undo", map[string]string{"t": tok}, "Undo the change"))
		return
	}
	u, err := db.UndoEmailChange(r.Context(), h.database, hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		gone()
		return
	case errors.Is(err, db.ErrUndoAddressTaken):
		writeMessagePage(w, r, "", http.StatusConflict, "That address is used by another account now",
			supportText("Write to support@simple-host.app and we will sort it out."), home, "Go to Simple Host", "")
		return
	case err != nil:
		log.Printf("email change undo: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("email change user_id=%s: undone from the old address", u.UserID)
	writeMessagePage(w, r, "", http.StatusOK, "Done: you sign in with "+html.EscapeString(u.OldEmail)+" again",
		"Every key was signed out. Sign in with this address to get back in, then check your sites and connected apps.", home, "Sign in", "")
}

// identityResponse is one linked Google or GitHub sign-in.
type identityResponse struct {
	ID       string    `json:"id"`
	Provider string    `json:"provider"`
	Email    string    `json:"email,omitempty"`
	LinkedAt time.Time `json:"linked_at"`
}

// listIdentities GET /v1/me/identities: the Google and GitHub accounts that
// sign in to this account. Own key only.
func (h *UserHandler) listIdentities(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "listing sign-ins")
	if user == nil {
		return
	}
	list, err := db.ListSignInIdentities(r.Context(), h.database, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	out := make([]identityResponse, 0, len(list))
	for _, i := range list {
		out = append(out, identityResponse{ID: i.ID, Provider: i.Provider, Email: i.Email, LinkedAt: i.LinkedAt.UTC()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"identities": out})
}

// unlinkIdentity DELETE /v1/me/identities/{id}: that Google or GitHub
// account no longer signs in here. Own key only.
func (h *UserHandler) unlinkIdentity(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "removing a sign-in")
	if user == nil {
		return
	}
	err := db.UnlinkSignInIdentity(r.Context(), h.database, user.ID, strings.TrimSpace(r.PathValue("id")))
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such sign-in on this account"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
