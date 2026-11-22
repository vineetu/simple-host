package siteshttp

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// The deploy routes: POST/PUT /v1/sites/{sitename} (an archive body) here and
// POST/PUT /v1/sites/{sitename}/files (JSON) in jsondeploy.go. Each one names
// its target, asks sites.AdmitDeploy for the body limit, reads the body under
// it and hands it to sites.Deploy; writeDeploy answers.

// createSite is POST /v1/sites/{sitename} with an archive body.
func (h *SiteHandler) createSite(w http.ResponseWriter, r *http.Request) { h.deployArchive(w, r, true) }

// updateSite is PUT /v1/sites/{sitename} with an archive body.
func (h *SiteHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	h.deployArchive(w, r, false)
}

func (h *SiteHandler) deployArchive(w http.ResponseWriter, r *http.Request, isNew bool) {
	target, ok := deployTarget(w, r, isNew)
	if !ok {
		return
	}
	adm, err := h.SitesSvc().AdmitDeploy(r.Context(), target)
	if err != nil {
		writeCreatePreflight(w, err)
		return
	}
	if !h.readArchiveCreateAccess(w, r) {
		return
	}
	body, err := ReadLimitedBody(w, r, adm.Limit)
	if err != nil {
		return
	}
	in := deployInput(r, target, adm)
	in.Archive, in.ContentDigest = body, r.Header.Get("X-Content-Digest")
	result, err := h.SitesSvc().Deploy(r.Context(), in)
	h.writeDeploy(w, r, in, result, err)
}

// deployTarget reads who deploys which site with which options. isNew is
// the create route (POST).
func deployTarget(w http.ResponseWriter, r *http.Request, isNew bool) (sites.DeployTarget, bool) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return sites.DeployTarget{}, false
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return sites.DeployTarget{}, false
	}
	target := sites.DeployTarget{User: user, Name: siteName, New: isNew, PublishParam: r.URL.Query().Get("publish")}
	if !isNew {
		target.CreateParam = r.URL.Query().Get("create")
	}
	return target, true
}

// deployInput is the request's part of every deploy: the access chosen at
// creation (readCreateAccess), the author, If-Match and the operator's site.
func deployInput(r *http.Request, target sites.DeployTarget, adm sites.Admission) sites.DeployInput {
	access := creationAccess(r)
	in := sites.DeployInput{DeployTarget: target, Admission: adm, Access: access.Access, Viewers: access.Viewers, Author: versionAuthor(r), IfMatch: r.Header.Get("If-Match")}
	in.ExpectedID, in.CheckID = r.Context().Value(web.AdminSiteKey{}).(string)
	return in
}

// writeDeploy answers a deploy: 201 for a new site, 200 for an update, and
// for one stored with publish=false the version it stored and a preview link.
func (h *SiteHandler) writeDeploy(w http.ResponseWriter, r *http.Request, in sites.DeployInput, result sites.DeployResult, err error) {
	switch {
	case err != nil:
		h.writeDeployError(w, r, in.Name, in.Limit, in.Archive != nil, err)
	case result.Created:
		h.countDeploy(r.Context(), in.User)
		httpx.WriteJSON(w, http.StatusCreated, h.siteResponse(result.Site, result.FileBytes))
	case !result.Published:
		h.writeUnpublished(w, result.Site, result.Version, result.FileBytes)
	default:
		h.noteDeployed(r.Context(), in.User, result.Site.ID, result.Version, result.PreviousVersion)
		httpx.WriteJSON(w, http.StatusOK, h.siteResponse(result.Site, result.FileBytes))
	}
}

// writeDeployError maps a deploy's refusal (sites.Deploy, or a photo batch's
// sites.Update) to its answer. limit is the upload limit the refusal names;
// archive says the upload was an archive.
func (h *SiteHandler) writeDeployError(w http.ResponseWriter, r *http.Request, siteName string, limit int64, archive bool, err error) {
	var siteErr *sites.Error
	var gateErr *sites.GateError
	switch {
	case errors.As(err, &gateErr):
		writeRollbackGateError(w, gateErr)
	case writePublishRefusal(w, err):
	case errors.As(err, new(*sites.UploadError)):
		h.writePreparationError(w, err, limit, archive)
	case !errors.As(err, &siteErr):
		httpx.WriteInternal(w)
	case siteErr.Code == "unauthorized":
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
	case siteErr.Code == "no_personal_sites":
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
	case siteErr.Code == "recently_deleted":
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": siteErr.Message, "code": "recently_deleted", "recently_deleted": true})
	case siteErr.Code == "site_exists":
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: siteErr.Message, Code: "site_exists"})
	case siteErr.Code == "deploy_site_not_found":
		writeDeploySiteNotFound(w, r, siteName)
	case siteErr.Code == "patch_too_large":
		writeTooLarge(w, limit, siteErr.Message)
	case siteErr.Code == "patch_invalid":
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: siteErr.Message})
	default:
		writeSitesError(w, err, siteName)
	}
}

var publishStatus = map[string]int{
	"named_viewers_unavailable": 409, "sign_in_unavailable": 409, "named_viewers_need_own_address": 409, "access_differs": 409,
	"too_many_viewers": 400, "redirects_invalid": 400, "access_marker": 500,
}

func writePublishRefusal(w http.ResponseWriter, err error) bool {
	var siteErr *sites.Error
	if !errors.As(err, &siteErr) {
		return false
	}
	if status, ok := publishStatus[siteErr.Code]; ok {
		httpx.WriteJSON(w, status, httpx.ErrorResponse{Code: siteErr.Code, Error: siteErr.Message})
		return true
	}
	switch siteErr.Code {
	case "site_total_too_large", "account_storage_full":
		httpx.WriteJSON(w, 413, httpx.ErrorResponse{Code: siteErr.Code, Error: siteErr.Message, Hint: sites.StorageTips})
	case "site_measure_failed", "account_measure_failed":
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: siteErr.Message})
	default:
		return false
	}
	return true
}

// writeCreatePreflight answers a deploy sites.AdmitDeploy refused.
func writeCreatePreflight(w http.ResponseWriter, err error) {
	var siteErr *sites.Error
	if errors.As(err, &siteErr) {
		switch siteErr.Code {
		case "site_quota_reached", "no_personal_sites":
			httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
			return
		case "name_reserved", "invalid_name", "invalid_request":
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
			return
		}
	}
	httpx.WriteInternal(w)
}

func ReadLimitedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeTooLarge(w, limit, "")
			return nil, err
		}

		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid request body"})
		return nil, err
	}

	if len(body) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "request body is required"})
		return nil, errors.New("empty request body")
	}

	return body, nil
}

func writeTooLarge(w http.ResponseWriter, limit int64, detail string) {
	e := sites.TooLarge(limit, detail)
	httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, httpx.ErrorResponse{Error: e.Message, Code: e.Code, Hint: sites.StorageTips})
}
