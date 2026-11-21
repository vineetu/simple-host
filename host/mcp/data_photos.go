package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

const photoLinkExpired = "This photo link has expired. Ask your chat for a new one."

// ServePhotoLink exposes only the fixed site and its photo upload. The handler
// registers it behind the account apex Origin check and IP rate limiter.
// The fragment token travels in a header, never a request URL or Referer.
func (s *Server) ServePhotoLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		httpx.WriteError(w, 405, "method_not_allowed", "Use the Add photos page.")
		return
	}
	reconcile := r.Method == http.MethodPost && r.Header.Get("X-Photo-Reconcile") == "1"
	hash := sha256.Sum256([]byte(r.Header.Get("X-Photo-Token")))
	link, ok := s.claimPhotoLink(w, hash, reconcile)
	if !ok {
		return
	}
	saved, ended := false, false
	defer func() { s.releasePhotoLink(hash, link, saved, ended) }()
	key, revoke, err := link.mint(r.Context())
	if err != nil {
		ended = true
		httpx.WriteError(w, 403, "photo_link_signed_out", photoLinkExpired)
		return
	}
	defer revoke()
	if r.Method == http.MethodGet {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"site": link.site})
		return
	}
	saved = s.forwardPhotos(w, r, link.site, key) && !reconcile
}

// claimPhotoLink marks a live photo link busy for one request, or answers
// why it cannot be used: expired, out of saves, or still saving.
func (s *Server) claimPhotoLink(w http.ResponseWriter, hash [sha256.Size]byte, reconcile bool) (uploadLink, bool) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	for hash, entry := range s.uploads {
		if !entry.expires.After(time.Now()) {
			delete(s.uploads, hash)
		}
	}
	link, ok := s.uploads[hash]
	if !ok || !link.photos || link.saves >= 10 && !reconcile {
		httpx.WriteError(w, 404, "photo_link_expired", photoLinkExpired)
		return uploadLink{}, false
	}
	if link.busy {
		httpx.WriteError(w, 409, "photo_link_busy", "Photos are still saving. Try again when they finish.")
		return uploadLink{}, false
	}
	link.busy = true
	s.uploads[hash] = link
	return link, true
}

// releasePhotoLink counts a save and frees the link, or drops it once it has
// ended or expired.
func (s *Server) releasePhotoLink(hash [sha256.Size]byte, link uploadLink, saved, ended bool) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	if saved {
		link.saves++
	}
	if ended || !link.expires.After(time.Now()) {
		delete(s.uploads, hash)
		return
	}
	link.busy = false
	s.uploads[hash] = link
}

// forwardPhotos sends the photo upload to the site's photo route with the
// link's fresh key and relays the answer, reporting whether it saved. No
// caller-controlled path, query, key, cookie, or destination is forwarded.
func (s *Server) forwardPhotos(w http.ResponseWriter, r *http.Request, site, key string) bool {
	// Set the deadline on the real writer: capture cannot propagate it.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(photoBodyTimeout()))
	defer rc.SetReadDeadline(time.Time{})
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "/v1/sites/"+url.PathEscape(site)+"/photos", r.Body)
	req.Host = s.cfg.APIHost
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Skill-Version", s.cfg.SkillVersion)
	for _, name := range []string{"Content-Type", "Origin", "X-Forwarded-For", "X-Photo-Reconcile"} {
		req.Header.Set(name, r.Header.Get(name))
	}
	req.ContentLength = r.ContentLength
	req.RemoteAddr = r.RemoteAddr
	response := &capture{header: make(http.Header), status: http.StatusOK}
	s.cfg.Upstream.ServeHTTP(response, req)
	if response.status < 200 || response.status >= 300 {
		writePhotoRefusal(w, response)
		return false
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body.Bytes())
	return true
}

// photoBodyTimeout is how long one upload's body may take to arrive.
func photoBodyTimeout() time.Duration {
	seconds, err := strconv.Atoi(os.Getenv("PHOTO_BODY_TIMEOUT_SECONDS"))
	if err != nil || seconds < 1 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

// photoRefusals replace owner-route wording, which may contain account-wide
// storage figures or advice to read account data; a photo link exposes neither.
var photoRefusals = map[int]struct{ code, message string }{
	401: {"photo_link_refused", "This link cannot save photos. Ask your chat for a new one."},
	403: {"photo_link_refused", "This link cannot save photos. Ask your chat for a new one."},
	404: {"photo_link_site_gone", "This site is no longer available. Ask your chat for a new link."},
	413: {"photo_link_too_large", "These photos exceed the photo or storage allowance. Ask your chat for help."},
}

func writePhotoRefusal(w http.ResponseWriter, response *capture) {
	var body struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(response.body.Bytes(), &body)
	if refusal, ok := photoRefusals[response.status]; ok {
		body.Code, body.Error = refusal.code, refusal.message
	}
	if body.Error == "" {
		body.Error = "Could not save these photos. Try again."
	}
	if retry := response.header.Get("Retry-After"); retry != "" {
		w.Header().Set("Retry-After", retry)
	}
	httpx.WriteError(w, response.status, body.Code, body.Error)
}
