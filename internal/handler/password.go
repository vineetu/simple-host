package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
)

func (h *UserHandler) SetPasswordSignIn(enabled, selfSignup bool) {
	h.passwordEnabled, h.passwordSelfSignup = enabled, selfSignup
	h.passwordLoginLimiter = newRateLimiter(10, 10.0/(15*60))
	h.passwordIPLimiter = newRateLimiterFor(h.ed.Limits().RateSigninIP)
	h.httpCaches.rate("http.user.passwordLoginLimiter", h.passwordLoginLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.rate("http.user.passwordIPLimiter", h.passwordIPLimiter, 10*time.Minute, 30*time.Minute)
}
func (h *UserHandler) registerPasswords(mux *http.ServeMux, authMW, noticeMW func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/auth/password", h.passwordRoute(accountAuthOnly(h.publicBaseURL, noticeMW(http.HandlerFunc(h.passwordSignIn)))))
	mux.Handle("POST /v1/auth/password/signup", h.passwordRoute(accountAuthOnly(h.publicBaseURL, noticeMW(http.HandlerFunc(h.passwordSignup)))))
	mux.Handle("POST /v1/admin/password-users", h.passwordRoute(noticeMW(authMW(http.HandlerFunc(h.createPasswordUser)))))
	mux.Handle("POST /v1/admin/users/{id}/password", h.passwordRoute(noticeMW(authMW(http.HandlerFunc(h.resetPassword)))))
	mux.Handle("POST /v1/me/password", h.passwordRoute(noticeMW(authMW(http.HandlerFunc(h.changePassword)))))
}
func (h *UserHandler) passwordRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.passwordEnabled {
			passwordNotFound(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func passwordNotFound(w http.ResponseWriter) { writeJSON(w, 404, errorResponse{Error: "not found"}) }
func (h *UserHandler) passwordSignIn(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled {
		passwordNotFound(w)
		return
	}
	var req struct {
		Login       string `json:"login"`
		Password    string `json:"password"`
		NewPassword string `json:"new_password"`
		Name        string `json:"name"`
		SSOState    string `json:"sso_state"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	if _, err := signInKeyName(verifyRequest{Name: req.Name}); err != nil {
		writeJSON(w, 400, errorResponse{Error: err.Error()})
		return
	}
	login, ip := accounts.NormalizeLogin(req.Login), clientIP(r)
	if _, blocked := h.geoBlock.BlockedCountry(ip); blocked {
		writeJSON(w, http.StatusForbidden, signupBlockedError)
		return
	}
	// Every attempt spends the per-IP budget (each one costs an argon2 hash);
	// only the per-login budget is given back when the password was right.
	if !h.passwordIPLimiter.allowHTTP(w, ip) {
		tooManyRequests(w)
		return
	}
	if !h.passwordLoginLimiter.allowHTTP(w, login) {
		h.passwordIPLimiter.Refund(ip)
		tooManyRequests(w)
		return
	}
	user, err := h.accounts.PasswordSignIn(r.Context(), login, req.Password, req.NewPassword)
	var failure *accounts.LifecycleError
	if !errors.As(err, &failure) || failure.Code != "bad_credentials" {
		h.passwordLoginLimiter.Refund(login)
	}
	if writeLifecycleFailure(w, err) {
		return
	}
	h.finishSignIn(w, r, user, false, verifyRequest{Name: req.Name, SSOState: req.SSOState})
}

// changePassword handles POST /v1/me/password {current_password,new_password}:
// a signed-in password account replaces its own password. Wrong credentials
// and successes share the sign-in limiters (login key: the account's
// normalized username), refunding the login budget only when the current
// password was right, same as passwordSignIn. Keys, sessions and connector
// tokens are untouched; only this account's other keys see no change.
func (h *UserHandler) changePassword(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled {
		passwordNotFound(w)
		return
	}
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if isConnectorRequest(r.Context()) {
		// A connected app's access token otherwise carries the person's full
		// REST permissions (BearerAuth), but changing the password itself
		// must come from the person signed in with their own account key.
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "Sign in on this site to change your password.", Code: "connector_not_allowed"})
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	login, ip := accounts.NormalizeLogin(user.Username), clientIP(r)
	if !h.passwordIPLimiter.allowHTTP(w, ip) {
		tooManyRequests(w)
		return
	}
	if !h.passwordLoginLimiter.allowHTTP(w, login) {
		h.passwordIPLimiter.Refund(ip)
		tooManyRequests(w)
		return
	}
	err := h.accounts.ChangePassword(r.Context(), user.ID, req.CurrentPassword, req.NewPassword)
	var failure *accounts.LifecycleError
	if !errors.As(err, &failure) || failure.Code != "bad_credentials" {
		h.passwordLoginLimiter.Refund(login)
	}
	if writeLifecycleFailure(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *UserHandler) assignPasswordHandle(ctx context.Context, q accounts.AttributionStore, id, name string, random bool) {
	assignHandle(ctx, q, id, name, random)
}
func (h *UserHandler) passwordSignup(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled || !h.passwordSelfSignup {
		passwordNotFound(w)
		return
	}
	if !h.ipLimiter.allowHTTP(w, clientIP(r)) {
		tooManyRequests(w)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	if _, blocked := h.geoBlock.BlockedCountry(clientIP(r)); blocked {
		writeJSON(w, http.StatusForbidden, signupBlockedError)
		return
	}
	user, _, err := h.accounts.CreatePasswordAccount(r.Context(), req.Username, req.Password, false, h.ed.Identity().RandomHandles, h.assignPasswordHandle)
	if writeLifecycleFailure(w, err) {
		return
	}
	h.finishSignIn(w, r, user, true, verifyRequest{Name: model.KeyNameDashboard})
}

// temporaryPasswordResponse includes login (account_passwords.login)
// whenever the account has one: a self sign-up's username is a synthetic
// "pw-..." value (CreatePasswordAccount), never what it actually signs in
// with, so admin.html's "Temporary password for <username>" must show
// login instead whenever resetPassword hands one back (a fresh
// createPasswordUser account has username == login already, so this is a
// no-op there).
func temporaryPasswordResponse(w http.ResponseWriter, user model.User, password, login string) {
	resp := map[string]string{"id": user.ID, "username": user.Username, "handle": user.Handle.String, "temporary_password": password, "message": "Save this password now. The person must choose a new password when signing in."}
	if login != "" {
		resp["login"] = login
	}
	writeJSON(w, 200, resp)
}
func (h *UserHandler) createPasswordUser(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled {
		passwordNotFound(w)
		return
	}
	if !accountAdmin(w, r) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	user, password, err := h.accounts.CreatePasswordAccount(r.Context(), req.Username, req.Password, true, h.ed.Identity().RandomHandles, h.assignPasswordHandle)
	if writeLifecycleFailure(w, err) {
		return
	}
	login, _, lerr := h.accounts.PasswordLogin(r.Context(), user.ID)
	if lerr != nil {
		log.Printf("auth: password login lookup after create: %v", lerr)
	}
	temporaryPasswordResponse(w, user, password, login)
}
func (h *UserHandler) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled {
		passwordNotFound(w)
		return
	}
	if !accountAdmin(w, r) {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	user, password, err := h.accounts.ResetPassword(r.Context(), r.PathValue("id"), req.Password)
	if writeLifecycleFailure(w, err) {
		return
	}
	login, _, lerr := h.accounts.PasswordLogin(r.Context(), user.ID)
	if lerr != nil {
		log.Printf("auth: password login lookup after reset: %v", lerr)
	}
	temporaryPasswordResponse(w, user, password, login)
}
