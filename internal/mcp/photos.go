package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

const photoLinkExpired = "This photo link has expired. Ask your chat for a new one."

// ServePhotoLink exposes only the fixed site and its photo upload. The handler
// registers it behind the account apex Origin check and IP rate limiter.
// The fragment token travels in a header, never a request URL or Referer.
func (s *Server) ServePhotoLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "application/json")
	fail := func(status int, code, message string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		fail(405, "method_not_allowed", "Use the Add photos page.")
		return
	}
	hash := sha256.Sum256([]byte(r.Header.Get("X-Photo-Token")))
	s.uploadMu.Lock()
	for hash, entry := range s.uploads {
		if !entry.expires.After(time.Now()) {
			delete(s.uploads, hash)
		}
	}
	link, ok := s.uploads[hash]
	if !ok || !link.photos || link.saves >= 10 {
		s.uploadMu.Unlock()
		fail(404, "photo_link_expired", photoLinkExpired)
		return
	}
	if link.busy {
		s.uploadMu.Unlock()
		fail(409, "photo_link_busy", "Photos are still saving. Try again when they finish.")
		return
	}
	link.busy = true
	s.uploads[hash] = link
	s.uploadMu.Unlock()
	saved, ended := false, false
	defer func() {
		s.uploadMu.Lock()
		defer s.uploadMu.Unlock()
		if saved {
			link.saves++
		}
		if ended || link.saves >= 10 || !link.expires.After(time.Now()) {
			delete(s.uploads, hash)
		} else {
			link.busy = false
			s.uploads[hash] = link
		}
	}()
	key, revoke, err := link.mint(r.Context(), link.team)
	if err != nil {
		ended = true
		fail(403, "photo_link_signed_out", photoLinkExpired)
		return
	}
	defer revoke()
	if r.Method == http.MethodGet {
		_ = json.NewEncoder(w).Encode(map[string]string{"site": link.site})
		return
	}
	// No caller-controlled path, query, key, cookie, or destination is forwarded.
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "/v1/sites/"+url.PathEscape(link.site)+"/photos", r.Body)
	req.Host = s.cfg.APIHost
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Skill-Version", s.cfg.SkillVersion)
	for _, name := range []string{"Content-Type", "Origin", "X-Forwarded-For"} {
		req.Header.Set(name, r.Header.Get(name))
	}
	req.ContentLength = r.ContentLength
	req.RemoteAddr = r.RemoteAddr
	response := &capture{header: make(http.Header), status: http.StatusOK}
	s.cfg.Upstream.ServeHTTP(response, req)
	saved = response.status >= 200 && response.status < 300
	if !saved {
		// Owner-route hints may contain account-wide storage figures or advice
		// to read account data. A photo link exposes neither.
		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		_ = json.Unmarshal(response.body.Bytes(), &body)
		switch response.status {
		case 401, 403:
			body.Error = "This link cannot save photos. Ask your chat for a new one."
		case 404:
			body.Error = "This site is no longer available. Ask your chat for a new link."
		case 413:
			body.Error = "These photos exceed the photo or storage allowance. Ask your chat for help."
		}
		if body.Error == "" {
			body.Error = "Could not save these photos. Try again."
		}
		if retry := response.header.Get("Retry-After"); retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		fail(response.status, body.Code, body.Error)
		return
	}
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body.Bytes())
}
