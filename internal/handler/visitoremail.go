package handler

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

func (h *SiteHandler) visitorEmailSite(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := strings.TrimSpace(r.PathValue("sitename"))
	if !h.authorizeStateOrigin(w, r, name) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return "", false
	}
	id, err := h.resolveSiteID(r, name)
	if err != nil {
		status, message := http.StatusInternalServerError, "internal server error"
		if errors.Is(err, sql.ErrNoRows) {
			status, message = http.StatusNotFound, "site not found"
		}
		writeJSON(w, status, errorResponse{Error: message})
		return "", false
	}
	if h.isVisitorApexHost(requestHostName(r)) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "not available on this host"})
		return "", false
	}
	return id, true
}

func (h *SiteHandler) requestVisitorEmail(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.visitorEmailSite(w, r)
	if !ok {
		return
	}
	if !h.visitorAuthLimiter.allow(clientIP(r)) {
		tooManyRequests(w)
		return
	}
	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	address, expires, status, body := issueEmailCode(r.Context(), h.database, h.mailer, h.emailLimiter, req.Email, "", "visitor", sql.NullString{String: siteID, Valid: true})
	if status != 0 {
		writeEmailCodeError(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, authChallengeResponse{Message: "Check your email for a sign-in code.", Email: address, ExpiresIn: expires})
}

func (h *SiteHandler) verifyVisitorEmail(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.visitorEmailSite(w, r)
	if !ok {
		return
	}
	if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing CSRF header", "code": "csrf_required"})
		return
	}
	if !h.visitorAuthLimiter.allow(clientIP(r)) {
		tooManyRequests(w)
		return
	}
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	user, _, status, body := verifyEmailCode(r.Context(), h.database, h.emailLimiter, verifyRequest{Email: req.Email, Code: req.Code}, "visitor", sql.NullString{String: siteID, Valid: true})
	if status != 0 {
		if status == http.StatusUnauthorized {
			writeJSON(w, status, map[string]string{"error": body.Error, "code": "invalid_code"})
		} else {
			writeEmailCodeError(w, status, body)
		}
		return
	}
	id, err := randomRaw(32)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	now := time.Now()
	expires, idle := now.Add(30*24*time.Hour), now.Add(14*24*time.Hour)
	if err := db.InsertVisitorSession(r.Context(), h.database, id, user.ID, siteID, requestHostName(r), expires, idle); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	setVisitorSessionCookie(w, r, hex.EncodeToString(id), visitorCookieMaxAge)
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, h.visitorSignedInResponse(r, idle, user.Username, "email"))
}

func (h *SiteHandler) optionsVisitorEmail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("sitename"))
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !h.authorizeStateOrigin(w, r, name) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-SH-CSRF")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}
