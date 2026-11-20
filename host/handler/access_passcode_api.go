package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

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
func (h *AccessHandler) ownerSiteForPasscode(w http.ResponseWriter, r *http.Request) (model.Site, func(), bool) {
	user := auth.GetUser(r.Context())
	name := strings.TrimSpace(r.PathValue("sitename"))
	if _, err := h.site.sitesSvc().GetSiteByUser(r.Context(), user.ID, name); err != nil {
		writeSiteLookupError(w, err)
		return model.Site{}, nil, false
	}
	unlock := h.site.lockSite(user.ID, name)
	site, err := h.site.sitesSvc().GetSiteByUser(r.Context(), user.ID, name)
	if err != nil {
		unlock()
		writeSiteLookupError(w, err)
		return model.Site{}, nil, false
	}
	if adminSiteMismatch(r, site.ID) {
		unlock()
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return model.Site{}, nil, false
	}
	return site, unlock, true
}

func writeSiteLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found", Code: "site_not_found"})
		return
	}
	writeInternal(w)
}

// getSitePasscode is GET /v1/sites/{sitename}/lock: whether the site has a
// passcode, since when, and the passcode itself (owner only; the admin's
// routes never read it).
func (h *AccessHandler) getSitePasscode(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	site, err := h.site.sitesSvc().GetSiteByUser(r.Context(), user.ID, strings.TrimSpace(r.PathValue("sitename")))
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	row, err := h.site.sitesSvc().GetSitePasscode(r.Context(), site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	resp := passcodeResponse{Site: site.Name, PasscodeProtected: row.Enc != nil}
	if row.Enc != nil {
		code, err := h.passcode.open(site.ID, row.Enc)
		if err != nil {
			log.Printf("passcode read site_id=%s: %v", site.ID, err)
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
	writeJSON(w, http.StatusOK, resp)
}

// putSitePasscode is PUT /v1/sites/{sitename}/lock with {"passcode": "..."}
// or {"generate": true}: sets or changes the passcode. A change signs
// everyone out; the same passcode again changes nothing.
func (h *AccessHandler) putSitePasscode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Passcode *string `json:"passcode"`
		Generate bool    `json:"generate"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Passcode == nil) == !req.Generate {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `invalid JSON body (expected {"passcode":"..."} or {"generate":true})`, Code: "invalid_request"})
		return
	}
	if !h.limits.SitePasscodes {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "site passcodes are not enabled on this server yet", Code: "passcodes_not_enabled"})
		return
	}
	if h.passcode.aead == nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "site passcodes are not enabled on this server (it has no PASSCODE_ENC_KEY)", Code: "passcodes_not_enabled"})
		return
	}
	var code string
	var err error
	if req.Generate {
		code, err = generatePasscode(h.limits.PasscodeMinLength)
		if err != nil {
			writeInternal(w)
			return
		}
	} else if code, err = normalizePasscode(*req.Passcode, h.limits.PasscodeMinLength); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_passcode"})
		return
	}
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if refuseSuspendedSite(w, site) {
		return
	}
	if site.NamedViewers {
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "this site is open only to named viewers; a site has named viewers or a passcode, not both. Set who can open it back to anyone first (set_site_access access anyone, or PUT /v1/sites/{sitename}/access), after the person agrees",
			Code:  "named_viewers_set",
		})
		return
	}
	if h.site.SharedOrigin() || contentHostOnlySites[site.OwnerHandle+"/"+site.Name] || !h.site.personAddressFor(site.OwnerHandle, site.Name) {
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: "a passcode needs the site to have an address of its own, and this site is served on an address every site shares",
			Code:  "passcode_needs_own_address",
		})
		return
	}
	row, err := h.site.sitesSvc().GetSitePasscode(r.Context(), site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	if row.Enc != nil {
		if cur, oerr := h.passcode.open(site.ID, row.Enc); oerr == nil && cur == code {
			resp := passcodeResponse{Site: site.Name, PasscodeProtected: true, Passcode: code, Note: "That is already this site's passcode; nothing changed."}
			if row.SetAt.Valid {
				t := row.SetAt.Time.UTC()
				resp.PasscodeSetAt = &t
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}
	enc, err := h.passcode.seal(site.ID, code)
	if err != nil {
		writeInternal(w)
		return
	}
	now, err := h.site.sitesSvc().SetSitePasscode(r.Context(), site, row, enc)
	if err != nil {
		writeAccessMutationError(w, err)
		return
	}

	changed := row.Enc != nil
	log.Printf("site_passcode set=true changed=%t generated=%t site_id=%s name=%s by=%s", changed, req.Generate, site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := "Every address of the site now asks for this passcode. Anyone you give it to can open the site and pass it on."
	if changed {
		note = "The passcode is changed: everyone who had entered the old one must enter the new one."
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: true, Passcode: code, PasscodeSetAt: &now, Note: note})
}

// deleteSitePasscode is DELETE /v1/sites/{sitename}/lock: the site opens to
// everyone again. Also the admin's (moderation), through asSiteOwner.
func (h *AccessHandler) deleteSitePasscode(w http.ResponseWriter, r *http.Request) {
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	row, err := h.site.sitesSvc().GetSitePasscode(r.Context(), site.ID)
	if err != nil {
		writeSiteLookupError(w, err)
		return
	}
	if err := h.site.sitesSvc().ClearSitePasscode(r.Context(), site); err != nil {
		writeAccessMutationError(w, err)
		return
	}

	log.Printf("site_passcode set=false had=%t site_id=%s name=%s by=%s", row.Enc != nil, site.ID, site.Name, auth.GetUser(r.Context()).ID)
	note := "The passcode is removed: every address of the site is open to everyone again."
	if row.Enc == nil {
		// Say so, so no one reports taking off a passcode that was never set.
		note = "This site had no passcode, so nothing changed."
	}
	writeJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: false, Note: note})
}

// signOutEveryone is POST /v1/sites/{sitename}/lock/sign-out-everyone:
// everyone who entered the passcode must enter it again.
func (h *AccessHandler) signOutEveryone(w http.ResponseWriter, r *http.Request) {
	site, unlock, ok := h.ownerSiteForPasscode(w, r)
	if !ok {
		return
	}
	defer unlock()
	if _, err := h.site.sitesSvc().BumpSitePasscodeGeneration(r.Context(), site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "this site has no passcode", Code: "no_passcode"})
			return
		}
		writeInternal(w)
		return
	}
	log.Printf("site_passcode sign_out_everyone site_id=%s name=%s by=%s", site.ID, site.Name, auth.GetUser(r.Context()).ID)
	writeJSON(w, http.StatusOK, passcodeResponse{Site: site.Name, PasscodeProtected: true, Note: "Everyone is signed out: each visitor must enter the passcode again."})
}
