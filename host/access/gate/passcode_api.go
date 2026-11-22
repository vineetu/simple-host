package gate

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/model"
)

// passcodeResponse is what the owner routes answer.
type passcodeResponse struct {
	Site              string     `json:"site"`
	PasscodeProtected bool       `json:"passcode_protected"`
	Passcode          string     `json:"passcode,omitempty"`
	PasscodeSetAt     *time.Time `json:"passcode_set_at,omitempty"`
	Note              string     `json:"note,omitempty"`
}

// ownerSiteForPasscode loads the caller's site by {sitename} under its lock
// for a change. ok=false after writing the answer.
func (h *Handler) ownerSiteForPasscode(w http.ResponseWriter, r *http.Request) (model.Site, func(), bool) {
	user := auth.GetUser(r.Context())
	name := strings.TrimSpace(r.PathValue("sitename"))
	if _, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, name); err != nil {
		web.WriteSiteLookupError(w, err)
		return model.Site{}, nil, false
	}
	unlock := sites.LockSiteName(h.UploadLocks, user.ID, name)
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, name)
	if err != nil {
		unlock()
		web.WriteSiteLookupError(w, err)
		return model.Site{}, nil, false
	}
	if web.AdminSiteMismatch(r, site.ID) {
		unlock()
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
		return model.Site{}, nil, false
	}
	return site, unlock, true
}

// getSitePasscode is GET /v1/sites/{sitename}/lock: whether the site has a
// passcode, since when, and the passcode itself (owner only; the admin's
// routes never read it).
func (h *Handler) getSitePasscode(w http.ResponseWriter, r *http.Request) {
	row, err := h.SitesSvc().GetSitePasscodeByName(r.Context(), auth.GetUser(r.Context()).ID, strings.TrimSpace(r.PathValue("sitename")))
	if err != nil {
		web.WriteSiteLookupError(w, err)
		return
	}
	resp := passcodeResponse{Site: row.Name, PasscodeProtected: row.Enc != nil}
	if row.Enc != nil {
		code, err := h.Passcode.open(row.SiteID, row.Enc)
		if err != nil {
			log.Printf("passcode read site_id=%s: %v", row.SiteID, err)
			resp.Note = "The passcode is set but cannot be read on this server (its key changed). Set a new one."
		} else {
			resp.Passcode = code
		}
		if row.SetAt.Valid {
			t := row.SetAt.Time.UTC()
			resp.PasscodeSetAt = &t
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// putSitePasscode is PUT /v1/sites/{sitename}/lock with {"passcode": "..."}
// or {"generate": true}: sets or changes the passcode. A change signs
// everyone out; the same passcode again changes nothing.
func (h *Handler) putSitePasscode(w http.ResponseWriter, r *http.Request) {
	var req passcodeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Passcode == nil) == !req.Generate {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `invalid JSON body (expected {"passcode":"..."} or {"generate":true})`, Code: "invalid_request"})
		return
	}
	code, err := h.chosenPasscode(req)
	if err != nil {
		web.WriteAccessError(w, err)
		return
	}
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if web.RefuseSuspendedSite(w, site) {
		return
	}
	resp, err := h.setPasscode(r, site, code, req.Generate)
	if err != nil {
		web.WriteAccessError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// passcodeRequest is PUT /lock's body: a passcode, or generate one.
type passcodeRequest struct {
	Passcode *string `json:"passcode"`
	Generate bool    `json:"generate"`
}

// chosenPasscode is the passcode a PUT /lock sets, once this server takes
// passcodes at all.
func (h *Handler) chosenPasscode(req passcodeRequest) (string, error) {
	if !h.Limits.SitePasscodes {
		return "", &accounts.Refusal{Status: http.StatusConflict, Code: "passcodes_not_enabled", Message: "site passcodes are not enabled on this server yet"}
	}
	if h.Passcode.keys.AEAD == nil {
		return "", &accounts.Refusal{Status: http.StatusConflict, Code: "passcodes_not_enabled", Message: "site passcodes are not enabled on this server (it has no PASSCODE_ENC_KEY)"}
	}
	if req.Generate {
		return generatePasscode(h.Limits.PasscodeMinLength)
	}
	code, err := normalizePasscode(*req.Passcode, h.Limits.PasscodeMinLength)
	if err != nil {
		return "", &accounts.Refusal{Status: http.StatusBadRequest, Code: "invalid_passcode", Message: err.Error()}
	}
	return code, nil
}

// gateSite is what the passcode and named-viewers rules need to know about
// a site on this server.
func (h *Handler) gateSite(site model.Site) accounts.GateSite {
	return h.Hosts.GateSite(site, h.noVisitorSignIn)
}

// setPasscode seals code as the site's passcode, unless it already is. The
// caller holds the site's lock.
func (h *Handler) setPasscode(r *http.Request, site model.Site, code string, generated bool) (passcodeResponse, error) {
	if err := accounts.PasscodeRefusal(site, h.gateSite(site)); err != nil {
		return passcodeResponse{}, err
	}
	row, err := h.SitesSvc().GetSitePasscode(r.Context(), site.ID)
	if err != nil {
		return passcodeResponse{}, err
	}
	if row.Enc != nil {
		if cur, oerr := h.Passcode.open(site.ID, row.Enc); oerr == nil && cur == code {
			resp := passcodeResponse{Site: site.Name, PasscodeProtected: true, Passcode: code, Note: "That is already this site's passcode; nothing changed."}
			if row.SetAt.Valid {
				t := row.SetAt.Time.UTC()
				resp.PasscodeSetAt = &t
			}
			return resp, nil
		}
	}
	enc, err := h.Passcode.Seal(site.ID, code)
	if err != nil {
		return passcodeResponse{}, err
	}
	now, err := h.SitesSvc().SetSitePasscode(r.Context(), site, row, enc)
	if err != nil {
		return passcodeResponse{}, err
	}
	changed := row.Enc != nil
	log.Printf("site_passcode set=true changed=%t generated=%t site_id=%s name=%s by=%s", changed, generated, site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := "Every address of the site now asks for this passcode. Anyone you give it to can open the site and pass it on."
	if changed {
		note = "The passcode is changed: everyone who had entered the old one must enter the new one."
	}
	return passcodeResponse{Site: site.Name, PasscodeProtected: true, Passcode: code, PasscodeSetAt: &now, Note: note}, nil
}

// DeleteSitePasscode is DELETE /v1/sites/{sitename}/lock: the site opens to
// everyone again. Also the admin's (moderation), through asSiteOwner.
func (h *Handler) DeleteSitePasscode(w http.ResponseWriter, r *http.Request) {
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	had, err := h.SitesSvc().ClearSitePasscode(r.Context(), site)
	if err != nil {
		web.WriteAccessMutationError(w, err)
		return
	}
	log.Printf("site_passcode set=false had=%t site_id=%s name=%s by=%s", had, site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := "The passcode is removed: every address of the site is open to everyone again."
	if !had {
		// Say so, so no one reports taking off a passcode that was never set.
		note = "This site had no passcode, so nothing changed."
	}
	httpx.WriteJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: false, Note: note})
}

// signOutEveryone is POST /v1/sites/{sitename}/lock/sign-out-everyone:
// everyone who entered the passcode must enter it again.
func (h *Handler) signOutEveryone(w http.ResponseWriter, r *http.Request) {
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if _, err := h.SitesSvc().BumpSitePasscodeGeneration(r.Context(), site.ID); err != nil {
		web.WriteAccessError(w, err)
		return
	}
	log.Printf("site_passcode sign_out_everyone site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	httpx.WriteJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: true, Note: "Everyone is signed out: each visitor must enter the passcode again."})
}
