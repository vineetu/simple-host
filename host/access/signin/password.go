package signin

import (
	"errors"
	"log"
	"net/http"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

func (h *Handler) SetPasswordSignIn(enabled, selfSignup bool) {
	h.passwordEnabled, h.passwordSelfSignup = enabled, selfSignup
	h.passwordLoginLimiter = httpx.NewRateLimiter(10, 10.0/(15*60))
	h.passwordIPLimiter = httpx.NewRateLimiterFor(h.ed.Limits().RateSigninIP.Burst, h.ed.Limits().RateSigninIP.Every)
	h.httpCaches.Rate("http.user.passwordLoginLimiter", h.passwordLoginLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.user.passwordIPLimiter", h.passwordIPLimiter, 10*time.Minute, 30*time.Minute)
}
func (h *Handler) registerPasswords(mux *http.ServeMux, authMW, noticeMW func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/auth/password", h.passwordRoute(web.AccountAuthOnly(h.publicBaseURL, noticeMW(http.HandlerFunc(h.passwordSignIn)))))
	mux.Handle("POST /v1/auth/password/signup", h.passwordRoute(web.AccountAuthOnly(h.publicBaseURL, noticeMW(http.HandlerFunc(h.passwordSignup)))))
	mux.Handle("POST /v1/admin/password-users", h.passwordRoute(noticeMW(authMW(http.HandlerFunc(h.createPasswordUser)))))
	mux.Handle("POST /v1/admin/users/{id}/password", h.passwordRoute(noticeMW(authMW(http.HandlerFunc(h.resetPassword)))))
	mux.Handle("POST /v1/me/password", h.passwordRoute(noticeMW(authMW(http.HandlerFunc(h.changePassword)))))
}
func (h *Handler) passwordRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.passwordEnabled {
			passwordNotFound(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func passwordNotFound(w http.ResponseWriter) {
	httpx.WriteJSON(w, 404, httpx.ErrorResponse{Error: "not found"})
}
func (h *Handler) passwordSignIn(w http.ResponseWriter, r *http.Request) {
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
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	if _, err := signInKeyName(VerifyRequest{Name: req.Name}); err != nil {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	login, ip := accounts.NormalizeLogin(req.Login), httpx.ClientIP(r)
	if _, blocked := h.geoBlock.BlockedCountry(ip); blocked {
		httpx.WriteJSON(w, http.StatusForbidden, web.SignupBlockedError)
		return
	}
	// Every attempt spends the per-IP budget (each one costs an argon2 hash);
	// only the per-login budget is given back when the password was right.
	if !h.passwordIPLimiter.AllowHTTP(w, ip) {
		httpx.TooManyRequests(w)
		return
	}
	if !h.passwordLoginLimiter.AllowHTTP(w, login) {
		h.passwordIPLimiter.Refund(ip)
		httpx.TooManyRequests(w)
		return
	}
	user, err := PasswordSignIn(r.Context(), h.database, login, req.Password, req.NewPassword)
	var failure *accounts.LifecycleError
	if !errors.As(err, &failure) || failure.Code != "bad_credentials" {
		h.passwordLoginLimiter.Refund(login)
	}
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	h.finishSignIn(w, r, user, false, VerifyRequest{Name: req.Name, SSOState: req.SSOState})
}

// changePassword handles POST /v1/me/password {current_password,new_password}:
// a signed-in password account replaces its own password. Wrong credentials
// and successes share the sign-in limiters (login key: the account's
// normalized username), refunding the login budget only when the current
// password was right, same as passwordSignIn. Keys, sessions and connector
// tokens are untouched; only this account's other keys see no change.
func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled {
		passwordNotFound(w)
		return
	}
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	if auth.IsConnectorRequest(r.Context()) {
		// A connected app's access token otherwise carries the person's full
		// REST permissions (BearerAuth), but changing the password itself
		// must come from the person signed in with their own account key.
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "Sign in on this site to change your password.", Code: "connector_not_allowed"})
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	login, ip := accounts.NormalizeLogin(user.Username), httpx.ClientIP(r)
	if !h.passwordIPLimiter.AllowHTTP(w, ip) {
		httpx.TooManyRequests(w)
		return
	}
	if !h.passwordLoginLimiter.AllowHTTP(w, login) {
		h.passwordIPLimiter.Refund(ip)
		httpx.TooManyRequests(w)
		return
	}
	err := ChangePassword(r.Context(), h.database, user.ID, req.CurrentPassword, req.NewPassword)
	var failure *accounts.LifecycleError
	if !errors.As(err, &failure) || failure.Code != "bad_credentials" {
		h.passwordLoginLimiter.Refund(login)
	}
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) passwordSignup(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled || !h.passwordSelfSignup {
		passwordNotFound(w)
		return
	}
	if !h.IPLimiter.AllowHTTP(w, httpx.ClientIP(r)) {
		httpx.TooManyRequests(w)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	if _, blocked := h.geoBlock.BlockedCountry(httpx.ClientIP(r)); blocked {
		httpx.WriteJSON(w, http.StatusForbidden, web.SignupBlockedError)
		return
	}
	user, _, err := h.accounts.CreatePasswordAccount(r.Context(), req.Username, req.Password, false, h.ed.Identity().RandomHandles, accounts.AssignHandle)
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	h.finishSignIn(w, r, user, true, VerifyRequest{Name: model.KeyNameDashboard})
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
	httpx.WriteJSON(w, 200, resp)
}
func (h *Handler) createPasswordUser(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled {
		passwordNotFound(w)
		return
	}
	if !auth.Admin(w, r) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	user, password, err := h.accounts.CreatePasswordAccount(r.Context(), req.Username, req.Password, true, h.ed.Identity().RandomHandles, accounts.AssignHandle)
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	login, _, lerr := h.accounts.PasswordLogin(r.Context(), user.ID)
	if lerr != nil {
		log.Printf("auth: password login lookup after create: %v", lerr)
	}
	temporaryPasswordResponse(w, user, password, login)
}
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	if !h.passwordEnabled {
		passwordNotFound(w)
		return
	}
	if !auth.Admin(w, r) {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	user, password, err := ResetPassword(r.Context(), h.database, r.PathValue("id"), req.Password)
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	login, _, lerr := h.accounts.PasswordLogin(r.Context(), user.ID)
	if lerr != nil {
		log.Printf("auth: password login lookup after reset: %v", lerr)
	}
	temporaryPasswordResponse(w, user, password, login)
}
