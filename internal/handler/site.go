package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/tarball"
)

const maxSiteArchiveSize = 100 << 20
const maxSiteStateSize = 1 << 20

// maxSitesPerUser caps how many sites a single non-admin account may create, so
// a self-registered user can't fill the shared disk with sites. Admins are
// exempt.
const maxSitesPerUser = 100

type SiteHandler struct {
	database     *sql.DB
	disk         *storage.DiskStorage
	siteDomain   string
	deployScript string

	// uploadLocks serializes write+promote per site name (sitename -> *sync.Mutex).
	uploadLocks sync.Map

	// uploadLimiter throttles create/update uploads per client IP; stateLimiter
	// throttles per-site state writes (which are intentionally unauthenticated —
	// see authorizeStateOrigin). See ratelimit.go.
	uploadLimiter *rateLimiter
	stateLimiter  *rateLimiter

	// locks is the in-memory view-password cache for the nginx auth_request gate.
	locks *viewLocks
	// viewSecret signs the view-session cookie (HMAC), so bcrypt runs only on
	// password submit, not on every page view.
	viewSecret []byte

	// previewAccounts (by username/email) get ephemeral sites: a site they create
	// expires after previewTTL and is removed by the background sweep. Empty =off.
	previewAccounts map[string]bool
	previewTTL      time.Duration
}

// lockSite acquires the per-site upload mutex and returns its unlock func.
func (h *SiteHandler) lockSite(name string) func() {
	mu, _ := h.uploadLocks.LoadOrStore(name, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

type siteResponse struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	Name           string    `json:"name"`
	ActiveVersion  int       `json:"active_version"`
	SiteURL        string    `json:"site_url"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	OwnerUsername  string    `json:"owner_username,omitempty"`
	Note           string    `json:"note,omitempty"`
}

type versionResponse struct {
	VersionNumber int       `json:"version_number"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	IsActive      bool      `json:"is_active"`
}

func NewSiteHandler(database *sql.DB, disk *storage.DiskStorage, siteDomain, deployScript, adminAPIKey string, previewAccounts map[string]bool, previewTTL time.Duration) *SiteHandler {
	// Uploads: ~6/min/IP, burst 30. State writes: ~1/s/IP sustained, burst 60
	// (a browser app may persist state on each interaction).
	uploadLimiter := newRateLimiter(30, 0.1)
	stateLimiter := newRateLimiter(60, 1)
	uploadLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	stateLimiter.startCleanup(10*time.Minute, 30*time.Minute)

	locks := newViewLocks()
	if m, err := db.LoadViewLocks(context.Background(), database); err != nil {
		log.Printf("viewauth: load locks at boot: %v", err)
	} else {
		locks.m = m
	}

	secret := sha256.Sum256([]byte(adminAPIKey + "|sh-view-cookie"))

	h := &SiteHandler{
		database:        database,
		disk:            disk,
		siteDomain:      siteDomain,
		deployScript:    deployScript,
		uploadLimiter:   uploadLimiter,
		stateLimiter:    stateLimiter,
		locks:           locks,
		viewSecret:      secret[:],
		previewAccounts: previewAccounts,
		previewTTL:      previewTTL,
	}
	if len(previewAccounts) > 0 {
		h.startExpirySweep(time.Hour)
		log.Printf("preview-site expiry enabled: accounts=%d ttl=%s", len(previewAccounts), previewTTL)
	}
	return h
}

// previewExpiry returns a per-site expiry timestamp when the owner is a
// configured preview account, or nil for a permanent site.
func (h *SiteHandler) previewExpiry(user *db.User) *time.Time {
	if user == nil || len(h.previewAccounts) == 0 || !h.previewAccounts[strings.ToLower(user.Username)] {
		return nil
	}
	t := time.Now().Add(h.previewTTL)
	return &t
}

// startExpirySweep periodically deletes sites whose expires_at has passed
// (DB rows + on-disk files), keeping the platform free of stale preview sites.
func (h *SiteHandler) startExpirySweep(every time.Duration) {
	go func() {
		// Run once shortly after boot, then on the interval.
		time.Sleep(30 * time.Second)
		for {
			h.sweepExpiredSites()
			time.Sleep(every)
		}
	}()
}

func (h *SiteHandler) sweepExpiredSites() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	expired, err := db.ListExpiredSites(ctx, h.database)
	if err != nil {
		log.Printf("expiry sweep: list failed: %v", err)
		return
	}
	for _, s := range expired {
		unlock := h.lockSite(s.Name)
		if err := db.DeleteSite(ctx, h.database, s.ID); err != nil {
			log.Printf("expiry sweep: delete row %s (%s): %v", s.Name, s.ID, err)
			unlock()
			continue
		}
		if err := h.disk.DeleteSite(s.Name); err != nil {
			log.Printf("expiry sweep: delete disk %s: %v", s.Name, err)
		}
		unlock()
		log.Printf("expiry sweep: removed preview site %q", s.Name)
	}
}

func (h *SiteHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.createSite)))))
	mux.Handle("PUT /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.updateSite)))))
	mux.Handle("DELETE /v1/sites/{sitename}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteSite))))
	mux.Handle("GET /v1/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSites))))
	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersions))))
	mux.Handle("PUT /v1/sites/{sitename}/active-version", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setActiveVersion))))

	// JSON deploy (LLM-friendly): file contents inline, no archive. Same auth +
	// rate-limit chain as the archive upload; CORS preflight is handled by the
	// CORS middleware (these paths do not end in /state).
	mux.Handle("POST /v1/sites/{sitename}/files", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.createSiteFiles)))))
	mux.Handle("PUT /v1/sites/{sitename}/files", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.updateSiteFiles)))))

	// State routes are deliberately NOT wrapped — they serve browser pages
	// that parse the JSON state object directly. Adding _notice would
	// corrupt that contract.
	//
	// TRUST MODEL: site state is PUBLIC per-site scratch storage. The pages
	// that use it run in the browser and hold no API key, so the only possible
	// gate is the Origin/Referer check (authorizeStateOrigin), which a real
	// browser cannot forge across sites but a non-browser client (curl) can.
	// We therefore treat state as readable/writable by anyone who knows the
	// site name: it has NO confidentiality or integrity guarantee — do not
	// store secrets in it, and don't trust it for security decisions. Abuse is
	// bounded by stateLimiter (rate) and maxSiteStateSize (1 MB cap).
	// View-lock (API-native): owner sets/clears a bcrypt view password; nginx
	// enforces it via auth_request -> /internal/view-auth (public access to
	// /internal is blocked at the apex proxy).
	mux.Handle("PUT /v1/sites/{sitename}/view-password", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setViewPassword))))
	mux.Handle("DELETE /v1/sites/{sitename}/view-password", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteViewPassword))))
	mux.HandleFunc("GET /internal/view-auth", h.viewAuth)
	mux.HandleFunc("GET /internal/view-login-page", h.viewLoginPage)
	mux.HandleFunc("POST /internal/view-login", h.viewLogin)

	// Append-only collections (second backend type): cheap O(1) appends +
	// paginated reads for large/high-volume lists. Origin-gated + view-lock
	// aware, like state.
	mux.HandleFunc("GET /v1/sites/{sitename}/collections/{coll}", h.listCollection)
	mux.Handle("POST /v1/sites/{sitename}/collections/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.appendCollection)))
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/collections/{coll}", h.optionsCollection)

	mux.HandleFunc("GET /v1/sites/{sitename}/state", h.getSiteState)
	mux.Handle("PUT /v1/sites/{sitename}/state", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.putSiteState)))
	mux.Handle("PATCH /v1/sites/{sitename}/state", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.patchSiteState)))
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/state", h.optionsSiteState)
}

// originHostForSite returns the expected hostname for state CORS, e.g.
// "mysite.simple-host.app".
func (h *SiteHandler) originHostForSite(siteName string) string {
	return siteName + "." + h.siteDomain
}

// authorizeStateOrigin checks Origin/Referer and, on a match, sets the CORS
// headers that allow the calling site to read the response. Returns true if
// the request is allowed.
func (h *SiteHandler) authorizeStateOrigin(w http.ResponseWriter, r *http.Request, siteName string) bool {
	want := h.originHostForSite(siteName)

	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}

	if origin == "" {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Host != want {
		return false
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	// Allow credentials so the page can send its view-session cookie on locked
	// sites (ACAO is the specific origin above, never "*", as credentials require).
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	// Expose ETag so the page's JS can read the state version for optimistic
	// concurrency (ETag is not a CORS-safelisted response header).
	w.Header().Set("Access-Control-Expose-Headers", "ETag")
	w.Header().Set("Vary", "Origin")
	return true
}

func (h *SiteHandler) optionsSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, PATCH, OPTIONS")
	// If-Match / If-None-Match carry the state version for optimistic-concurrency
	// PUTs and conditional GETs; PATCH sends ops as JSON.
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-Match, If-None-Match")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) getSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}
	if !h.viewSessionOK(r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this site is private — view it first to unlock its data"})
		return
	}

	// Conditional GET: if the caller already has the current version, do a cheap
	// version-only check and return 304 — no fetch/serialize of the document.
	// Makes pollers (e.g. the feedback overlay) nearly free on CPU.
	if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm != "" {
		if expected, ok := parseIfMatch(inm); ok {
			ver, err := db.GetSiteStateVersion(r.Context(), h.database, siteName)
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
				return
			}
			if err == nil && ver == expected {
				w.Header().Set("ETag", stateETag(ver))
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	state, version, err := db.GetSiteState(r.Context(), h.database, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(version))
	w.WriteHeader(http.StatusOK)
	w.Write(state)
}

// stateETag formats a state version as a (strong) ETag value.
func stateETag(version int) string {
	return `"` + strconv.Itoa(version) + `"`
}

// parseIfMatch parses an If-Match header value (e.g. `"7"` or `W/"7"`) into the
// expected version. Returns ok=false if it isn't a simple version token.
func parseIfMatch(v string) (int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "W/")
	v = strings.Trim(v, `"`)
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (h *SiteHandler) putSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}
	if !h.viewSessionOK(r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this site is private — view it first to unlock its data"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSiteStateSize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json"})
		return
	}

	state := json.RawMessage(body)

	// Optimistic concurrency is OPT-IN: if the caller sends If-Match, we only
	// write when the stored version matches (compare-and-swap). Without it,
	// behavior is the historical last-write-wins.
	var newVersion int
	if ifMatch := r.Header.Get("If-Match"); strings.TrimSpace(ifMatch) != "" {
		expected, ok := parseIfMatch(ifMatch)
		if !ok {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid If-Match header"})
			return
		}
		newVersion, err = db.UpdateSiteStateCAS(r.Context(), h.database, siteName, state, expected)
		if errors.Is(err, db.ErrStateVersionConflict) {
			// Hand back the current version so the client can re-read and retry.
			if _, cur, gerr := db.GetSiteState(r.Context(), h.database, siteName); gerr == nil {
				w.Header().Set("ETag", stateETag(cur))
			}
			writeJSON(w, http.StatusPreconditionFailed, errorResponse{Error: "state version conflict — re-read and retry"})
			return
		}
	} else {
		newVersion, err = db.UpdateSiteState(r.Context(), h.database, siteName, state)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(newVersion))
	w.WriteHeader(http.StatusOK)
	w.Write(state)
}

func (h *SiteHandler) createSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	// Reserved-name check is create-only: existing sites must always remain
	// re-deployable even if a name later lands on the reserved list.
	if err := validateSiteReserved(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	// Per-user site quota (admins exempt). Checked before we read the upload so
	// an over-quota request is cheap to reject. Updates to existing sites are
	// not affected — this only gates new-site creation.
	if !user.IsAdmin {
		existing, err := db.ListSitesByUser(r.Context(), h.database, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if len(existing) >= maxSitesPerUser {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "site quota reached"})
			return
		}
	}

	files, archiveSHA, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitNewSite(w, r, user, siteName, files, archiveSHA)
}

// commitNewSite writes a brand-new site's first version and promotes it after
// the DB commit. Shared by the archive upload (createSite) and the JSON upload
// (createSiteFiles) so both inherit identical versioning, locking, commit-then-
// promote ordering, and deploy-queue behavior.
func (h *SiteHandler) commitNewSite(w http.ResponseWriter, r *http.Request, user *db.User, siteName string, files map[string][]byte, archiveSHA string) {
	// Serialize all write+promote activity for this site so concurrent uploads
	// cannot race on version numbers or the `current` swap.
	unlock := h.lockSite(siteName)
	defer unlock()

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	siteURL := fmt.Sprintf("https://%s.%s", siteName, h.siteDomain)
	site, err := db.CreateSite(r.Context(), tx, user.ID, siteName, siteURL, h.previewExpiry(user))
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "site already exists"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	const versionNumber = 1
	diskPath := fmt.Sprintf("%s/v%d/", siteName, versionNumber)

	version, err := db.CreateVersion(r.Context(), tx, site.ID, versionNumber, diskPath, archiveSHA)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Write the new version dir (not yet live) before committing.
	if err := h.disk.WriteFiles(r.Context(), siteName, versionNumber, files); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.ActivateVersion(r.Context(), tx, version.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), tx, site.ID, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Promote only AFTER the DB is durable: a commit failure leaves `current`
	// untouched (pointing at the last good version) rather than half-swapped.
	if err := h.disk.UpdateCurrent(siteName, versionNumber); err != nil {
		log.Printf("createSite: promote %s v%d after commit: %v", siteName, versionNumber, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	site.ActiveVersion = versionNumber

	// Queue for cortex-share registration (processed by deploy-watcher)
	if h.deployScript != "" {
		queueFile := h.disk.DataDir() + "/.deploy-queue"
		if err := appendToFile(queueFile, siteName); err != nil {
			log.Printf("failed to queue deploy for %s: %v", siteName, err)
		}
	}

	writeJSON(w, http.StatusCreated, toSiteResponse(site, ""))
}

func (h *SiteHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	// Charset/shape only on update — never the reserved-name denylist, so an
	// existing site is always re-deployable.
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	files, archiveSHA, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitSiteUpdate(w, r, user, siteName, files, archiveSHA)
}

// commitSiteUpdate appends a new version to an existing owned site and promotes
// it after commit. Shared by the archive upload (updateSite) and the JSON upload
// (updateSiteFiles).
func (h *SiteHandler) commitSiteUpdate(w http.ResponseWriter, r *http.Request, user *db.User, siteName string, files map[string][]byte, archiveSHA string) {
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Serialize write+promote for this site (in-process), and take a DB row
	// lock so version allocation is safe even across multiple binary instances.
	unlock := h.lockSite(siteName)
	defer unlock()

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	if err := db.LockSiteForUpdate(r.Context(), tx, site.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	maxVersion, err := db.GetMaxVersionNumber(r.Context(), tx, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	versionNumber := maxVersion + 1
	diskPath := fmt.Sprintf("%s/v%d/", siteName, versionNumber)

	version, err := db.CreateVersion(r.Context(), tx, site.ID, versionNumber, diskPath, archiveSHA)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Write the new version dir (not yet live) before committing.
	if err := h.disk.WriteFiles(r.Context(), siteName, versionNumber, files); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.ActivateVersion(r.Context(), tx, version.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), tx, site.ID, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Promote only AFTER commit: if the DB never committed, `current` still
	// points at the previous good version instead of a half-applied swap.
	if err := h.disk.UpdateCurrent(siteName, versionNumber); err != nil {
		log.Printf("updateSite: promote %s v%d after commit: %v", siteName, versionNumber, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	site.ActiveVersion = versionNumber
	writeJSON(w, http.StatusOK, toSiteResponse(site, ""))
}

// filesRequest is the JSON deploy body: a map of relative path -> file contents.
// This is the LLM-friendly path — a web LLM can emit one copy-paste request with
// the file contents inline, no archiving step. For binary assets or large sites,
// use the archive upload (POST/PUT /v1/sites/{name}).
type filesRequest struct {
	Files map[string]string `json:"files"`
}

// readJSONFiles decodes a {"files":{path:contents}} body and runs it through the
// SAME path/secret/size guards as archive extraction (tarball.SanitizeFiles +
// ValidateExtensions). Returns the file map and a content digest for the version
// row. On any error it has already written the HTTP response.
func (h *SiteHandler) readJSONFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteArchiveSize)

	var req filesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return nil, "", err
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
		return nil, "", err
	}
	if len(req.Files) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "files object is required and must be non-empty"})
		return nil, "", errors.New("empty files")
	}

	raw := make(map[string][]byte, len(req.Files))
	for path, contents := range req.Files {
		raw[path] = []byte(contents)
	}

	files, err := tarball.SanitizeFiles(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, "", err
	}
	if err := tarball.ValidateExtensions(files); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, "", err
	}

	return files, digestFiles(files), nil
}

// digestFiles produces a stable SHA-256 over the (sorted) path+content stream,
// recorded on the version row for integrity/audit — the JSON analogue of the
// archive's body digest.
func digestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write(files[name])
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// createSiteFiles is the JSON create path. Mirrors createSite's pre-checks, then
// shares commitNewSite.
func (h *SiteHandler) createSiteFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := validateSiteReserved(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	if !user.IsAdmin {
		existing, err := db.ListSitesByUser(r.Context(), h.database, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if len(existing) >= maxSitesPerUser {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "site quota reached"})
			return
		}
	}

	files, digest, err := h.readJSONFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitNewSite(w, r, user, siteName, files, digest)
}

// updateSiteFiles is the JSON update path. Mirrors updateSite's pre-checks, then
// shares commitSiteUpdate.
func (h *SiteHandler) updateSiteFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	files, digest, err := h.readJSONFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitSiteUpdate(w, r, user, siteName, files, digest)
}

// listVersions returns the full version history of a site for the
// authenticated owner. Each row includes the version number, status,
// created_at, and a flag for which one is currently active.
func (h *SiteHandler) listVersions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	out := make([]versionResponse, len(versions))
	for i, v := range versions {
		out[i] = versionResponse{
			VersionNumber: v.VersionNumber,
			Status:        v.Status,
			CreatedAt:     v.CreatedAt,
			IsActive:      v.VersionNumber == site.ActiveVersion,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// setActiveVersion points the site's `current` symlink at the requested
// version and updates sites.active_version. Used for rollbacks (target
// older) and roll-forwards (target newer). Idempotent if the target is
// already active.
func (h *SiteHandler) setActiveVersion(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	var req struct {
		VersionNumber int `json:"version_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if req.VersionNumber < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "version_number must be >= 1"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if site.ActiveVersion == req.VersionNumber {
		writeJSON(w, http.StatusOK, toSiteResponse(site, ""))
		return
	}

	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	var found bool
	for _, v := range versions {
		if v.VersionNumber == req.VersionNumber {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
		return
	}

	if err := h.disk.UpdateCurrent(site.Name, req.VersionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), h.database, site.ID, req.VersionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	site.ActiveVersion = req.VersionNumber
	writeJSON(w, http.StatusOK, toSiteResponse(site, ""))
}

func (h *SiteHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	if err := db.DeleteSite(r.Context(), tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.DeleteSite(site.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) listSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	var sites []db.Site
	var err error

	if user.IsAdmin {
		sites, err = db.ListAllSites(r.Context(), h.database)
	} else {
		sites, err = db.ListSitesByUser(r.Context(), h.database, user.ID)
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	response := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		response = append(response, toSiteResponse(site, ""))
	}

	writeJSON(w, http.StatusOK, response)
}

// readAndValidateFiles reads the archive body, verifies an optional
// X-Content-Digest header against the SHA-256 of the raw archive bytes,
// extracts and validates the entries, and returns the files plus the hex
// digest (recorded on the version row for integrity/audit).
func (h *SiteHandler) readAndValidateFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	body, err := readLimitedBody(w, r)
	if err != nil {
		return nil, "", err
	}

	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	// Optional integrity check: if the client declares a digest, it must match
	// the bytes we received. Compared in constant time.
	if hdr := strings.TrimSpace(r.Header.Get("X-Content-Digest")); hdr != "" {
		want := strings.ToLower(strings.TrimPrefix(hdr, "sha256="))
		if subtle.ConstantTimeCompare([]byte(want), []byte(digest)) != 1 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "archive digest mismatch"})
			return nil, "", errors.New("archive digest mismatch")
		}
	}

	filename := archiveFilename(siteName, body)
	files, err := tarball.Extract(bytes.NewReader(body), filename)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site archive"})
		return nil, "", err
	}

	if err := tarball.ValidateExtensions(files); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, "", err
	}

	return files, digest, nil
}

func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteArchiveSize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return nil, err
		}

		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return nil, err
	}

	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "request body is required"})
		return nil, errors.New("empty request body")
	}

	return body, nil
}

func archiveFilename(siteName string, body []byte) string {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("PK\x03\x04")) {
		return siteName + ".zip"
	}

	return siteName + ".tar.gz"
}

func toSiteResponse(site db.Site, note string) siteResponse {
	return siteResponse{
		ID:            site.ID,
		UserID:        site.UserID,
		Name:          site.Name,
		ActiveVersion: site.ActiveVersion,
		SiteURL:       site.SiteURL,
		CreatedAt:     site.CreatedAt,
		UpdatedAt:     site.UpdatedAt,
		OwnerUsername: site.OwnerUsername,
		Note:          note,
	}
}

func appendToFile(path, line string) error {
	// Defense-in-depth: a newline would inject an extra line into the
	// deploy queue that the root deploy-watcher consumes. Site names are
	// already charset-validated upstream, but never rely solely on that.
	if strings.ContainsAny(line, "\n\r") {
		return errors.New("invalid queue entry")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}
