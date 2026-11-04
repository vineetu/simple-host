package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/app/accounts"
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
	login, ip := accounts.NormalizeLogin(req.Login), clientIP(r)
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
		h.passwordIPLimiter.Refund(ip)
		h.passwordLoginLimiter.Refund(login)
	}
	if writeLifecycleFailure(w, err) {
		return
	}
	h.finishSignIn(w, r, user, false, verifyRequest{Name: req.Name, SSOState: req.SSOState})
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
func temporaryPasswordResponse(w http.ResponseWriter, user model.User, password string) {
	writeJSON(w, 200, map[string]string{"id": user.ID, "username": user.Username, "handle": user.Handle.String, "temporary_password": password, "message": "Save this password now. The person must choose a new password when signing in."})
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
	temporaryPasswordResponse(w, user, password)
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
	temporaryPasswordResponse(w, user, password)
}
