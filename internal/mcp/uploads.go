package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"
)

const archiveUploadDescription = "For coding agents with a shell, when a site is too large for files_base64 (many photos or a moved site). Creates the site if it does not exist, else publishes a new version. The site is public at its address as soon as the upload succeeds. The link works once, for this site only, for 15 minutes. Never paste it into a web page or share it."

type uploadLink struct {
	photos  bool
	saves   int
	busy    bool
	owner   string
	site    string
	expires time.Time
	team    bool
	mint    func(context.Context, bool) (string, func(), error)
}

func createUploadLink(c *call, args map[string]any) (output, error) {
	archive, ok := args["archive"].(bool)
	if _, present := args["archive"]; present && !ok {
		return output{}, errors.New("archive must be a boolean")
	}
	archive = archive || c.caller.Mode == CallerModeHackSite || c.caller.Mode == CallerModeEvents
	site, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	now := time.Now()
	owner := c.caller.Owner
	if owner == "" {
		owner = c.caller.APIKey
	}
	mint := c.caller.MintUpload
	if mint == nil {
		key := c.caller.APIKey
		mint = func(context.Context, bool) (string, func(), error) { return key, func() {}, nil }
	}
	link := uploadLink{owner: owner, site: site, expires: now.Add(15 * time.Minute),
		team: c.caller.TeamAPIKey != "" && c.caller.APIKey == c.caller.TeamAPIKey, mint: mint}
	link.photos = !archive
	if link.photos {
		link.expires = now.Add(time.Hour)
		if c.caller.MintPhoto != nil {
			link.mint = c.caller.MintPhoto
		} else if expiry := c.caller.CredentialExpiresAt; !expiry.IsZero() && expiry.Before(link.expires) {
			link.expires = expiry
		}
	}
	if link.photos && !c.textOnlyClient {
		for _, screen := range c.server.enabledScreens(c.caller) {
			if screen.ID == "photo-picker" {
				return photoPickerOutput("https://"+c.server.cfg.APIHost+"/photos?site="+url.QueryEscape(site)+"&account="+url.QueryEscape(c.caller.Handle), link.expires), nil
			}
		}
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return output{}, errors.New("could not create an upload link")
	}
	token := base64.RawURLEncoding.EncodeToString(bytes[:])
	s := c.server
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	count := 0
	for hash, entry := range s.uploads {
		if !entry.expires.After(now) {
			delete(s.uploads, hash)
		} else if entry.owner == link.owner {
			count++
		}
	}
	if count >= 20 {
		return output{}, errors.New("there are already 20 live upload links for this account; use one or wait for it to expire before asking for another")
	}
	if s.uploads == nil {
		s.uploads = make(map[[32]byte]uploadLink)
	}
	s.uploads[sha256.Sum256([]byte(token))] = link
	if link.photos {
		return photoLinkOutput("https://"+s.cfg.APIHost+"/photos?site="+url.QueryEscape(site)+"#t="+token, link.expires), nil
	}
	uploadURL := "https://" + s.cfg.APIHost + "/v1/uploads/" + token
	out := map[string]any{
		"upload_url": uploadURL,
		"method":     "PUT",
		"expires_at": link.expires.UTC().Format(time.RFC3339),
		"how":        "curl -sS --fail-with-body -T site.tar.gz '" + uploadURL + "'\nBuild the site into a folder, pack it with tar -czf site.tar.gz -C <folder> . (or a .zip), send it with this command from a terminal, then read the JSON answer (site_url, active_version).",
	}
	return output{Text: jsonText(out), Structured: out}, nil
}

func photoPickerOutput(address string, expires time.Time) output {
	out := map[string]any{
		"photos_url": address,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"folder":     "images/",
		"picker":     "shown",
		"next":       "The photo picker is open in the chat. Tell the person to pick their photos there (photos_url opens the same page after sign-in). Each is shrunk and saved into images/. When they say done, call get_site and add the new images/ files to the page with update_site (keep_other_files: true).",
	}
	return output{Text: jsonText(out), Structured: out}
}

func photoLinkOutput(address string, expires time.Time) output {
	out := map[string]any{
		"photos_url": address,
		"expires_at": expires.UTC().Format(time.RFC3339),
		"folder":     "images/",
		"next":       "Give this link to the person. They pick photos on their phone or computer; each is shrunk and saved into images/. When they say done, call get_site and add the new images/ files to the page with update_site (keep_other_files: true).",
	}
	return output{Text: jsonText(out), Structured: out}
}

// ServeUpload consumes the link, re-checks sign-in, and streams one fixed deploy request.
func (s *Server) ServeUpload(w http.ResponseWriter, r *http.Request) {
	hash := sha256.Sum256([]byte(r.PathValue("token")))
	s.uploadMu.Lock()
	for hash, entry := range s.uploads {
		if !entry.expires.After(time.Now()) {
			delete(s.uploads, hash)
		}
	}
	link, ok := s.uploads[hash]
	if ok && link.photos {
		ok = false
	} else {
		delete(s.uploads, hash)
	}
	s.uploadMu.Unlock()
	if !ok || !link.expires.After(time.Now()) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "this upload link is not valid (it works once, for 15 minutes); ask your agent for a new one",
			"code":  "upload_link_invalid",
		})
		return
	}
	key, revoke, err := link.mint(r.Context(), link.team)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "this upload link's sign-in has ended (signed out, disconnected or expired); sign in again and ask your agent for a new link",
			"code":  "upload_link_signed_out",
		})
		return
	}
	defer revoke()
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPut, "/v1/sites/"+url.PathEscape(link.site)+"?create=1", r.Body)
	req.Host = s.cfg.APIHost
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Skill-Version", s.cfg.SkillVersion)
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	req.ContentLength = r.ContentLength
	if length := r.Header.Get("Content-Length"); length != "" {
		req.Header.Set("Content-Length", length)
	}
	req.RemoteAddr = r.RemoteAddr
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	s.cfg.Upstream.ServeHTTP(&uploadResponse{ResponseWriter: w, header: make(http.Header)}, req)
}

// Only the deploy status, content type and streamed body cross this boundary.
type uploadResponse struct {
	http.ResponseWriter
	header http.Header
	wrote  bool
}

func (w *uploadResponse) Header() http.Header { return w.header }
func (w *uploadResponse) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if value := w.header.Get("Content-Type"); value != "" {
		w.ResponseWriter.Header().Set("Content-Type", value)
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *uploadResponse) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
