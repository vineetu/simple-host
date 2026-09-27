package handler

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// Change sign-in email: POST /v1/me/email sends a code to the new address;
// POST /v1/me/email/verify with that code moves the account there and tells
// the old address. Only the person's own key: a connected app must not be
// able to move the account out from under its owner.
//
// "Another account already uses that address" is said only after the code is
// verified, i.e. to someone who reads that inbox, which is what sign-in
// itself reveals (it answers created: false). Asking for a code looks the same
// for every address.

type emailChangeRequest struct {
	Email string `json:"email"`
}

type emailChangeVerifyRequest struct {
	Code string `json:"code"`
}

func hashEmailChangeCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func (h *UserHandler) requestEmailChange(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "changing the sign-in email")
	if user == nil {
		return
	}
	if h.reviewerEmail != "" && strings.EqualFold(user.Username, h.reviewerEmail) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this account's email is set by the operator", Code: "reviewer_account"})
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
	if address == strings.ToLower(user.Username) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "that is already your sign-in email", Code: "same_email"})
		return
	}
	// The same throttles as sign-in: per new address (no mail-bombing an
	// inbox) and per account.
	if !h.emailLimiter.allow(emailLimiterKey(address)) || !h.emailLimiter.allow("email-change:"+user.ID) {
		writeEmailCodeError(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down"})
		return
	}
	code, err := generateNumericCode(6)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := db.PutEmailChange(r.Context(), h.database, user.ID, address, hashEmailChangeCode(code), time.Now().Add(authTokenTTL)); err != nil {
		log.Printf("email change user_id=%s: store: %v", user.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := h.sendEmailChangeCode(address, code); err != nil {
		log.Printf("email change: send code to %s: %v", redactEmail(address), err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not send verification email"})
		return
	}
	writeJSON(w, http.StatusAccepted, authChallengeResponse{
		Message:   "Check " + address + " for a 6-digit code, then send it to POST /v1/me/email/verify.",
		Email:     address,
		ExpiresIn: int(authTokenTTL.Seconds()),
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

It expires in 15 minutes. If you didn't ask for this, ignore this email: nothing changes.

Simple Host
`)
}

func (h *UserHandler) verifyEmailChange(w http.ResponseWriter, r *http.Request) {
	user := accountKeyUser(w, r, "changing the sign-in email")
	if user == nil {
		return
	}
	var req emailChangeVerifyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	code := strings.ReplaceAll(strings.TrimSpace(req.Code), "-", "")
	if code == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "code is required"})
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
	if subtle.ConstantTimeCompare([]byte(hashEmailChangeCode(code)), []byte(change.CodeHash)) != 1 {
		if err := db.CountEmailChangeAttempt(ctx, tx, user.ID); err == nil {
			_ = tx.Commit()
		}
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid or expired code", Code: "invalid_code"})
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
	err = db.ApplyEmailChange(ctx, tx, user.ID, oldEmail, change.NewEmail)
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
	h.emailOldAddress(oldEmail, change.NewEmail)
	writeJSON(w, http.StatusOK, map[string]string{
		"username": change.NewEmail,
		"email":    change.NewEmail,
		"message":  "Your sign-in email is now " + change.NewEmail + ". Sign in with it from now on; a notice went to your old address.",
	})
}

// emailOldAddress tells the old address where the account went, masked.
func (h *UserHandler) emailOldAddress(oldEmail, newEmail string) {
	mailer, ok := h.mailer.(noticeSender)
	if !ok || !strings.Contains(oldEmail, "@") {
		return
	}
	text := "Your Simple Host sign-in email was changed to " + maskEmail(newEmail) + `. From now on you sign in with that address.

If this wasn't you, write to support@simple-host.app.

Simple Host
`
	h.alerts.run(func() {
		if err := mailer.SendNotice(oldEmail, "Your Simple Host sign-in email was changed", text); err != nil {
			log.Printf("email change: notice to old address: %v", err)
		}
	})
}
