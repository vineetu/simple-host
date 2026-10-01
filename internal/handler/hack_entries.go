package handler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// Entries: the text, links and screenshot a team submits, kept apart from
// the team site (hosted events, M2; docs/designs/simple-hack-platform.md).
// A participant reads and writes only their own team's entry. Organisers
// and judges read every team's entry and screenshots.

// hackScreenshotMax is the largest screenshot stored: 2 MiB.
const hackScreenshotMax = 2 << 20

// hackScreenshotMaxSide is the largest width or height a screenshot may
// declare.
const hackScreenshotMaxSide = 8000

func (h *HackHandler) registerEntries(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/entry", wrap(h.getEntry))
	mux.Handle("PUT /v1/hack/events/{slug}/entry", wrap(h.putEntry))
	mux.Handle("GET /v1/hack/events/{slug}/entry/screenshot", wrap(h.getEntryScreenshot))
	mux.Handle("PUT /v1/hack/events/{slug}/entry/screenshot", wrap(h.putEntryScreenshot))
	mux.Handle("DELETE /v1/hack/events/{slug}/entry/screenshot", wrap(h.deleteEntryScreenshot))
	mux.Handle("GET /v1/hack/events/{slug}/teams/{team}/screenshot", wrap(h.getTeamScreenshot))
	mux.Handle("GET /v1/hack/events/{slug}/entries", wrap(h.listEntries))
}

func (h *HackHandler) getEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, team.ID)
}

func (h *HackHandler) putEntry(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	var req struct {
		Title       *string `json:"title"`
		Tagline     *string `json:"tagline"`
		Description *string `json:"description"`
		VideoURL    *string `json:"video_url"`
		CodeURL     *string `json:"code_url"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	fields := map[string]string{}
	if req.Title != nil {
		s, ok := checkHackLine(w, *req.Title, "title", 0, 80)
		if !ok {
			return
		}
		fields["title"] = s
	}
	if req.Tagline != nil {
		s, ok := checkHackLine(w, *req.Tagline, "tagline", 0, 160)
		if !ok {
			return
		}
		fields["tagline"] = s
	}
	if req.Description != nil {
		s, ok := checkHackText(w, *req.Description, "description", 0, 5000)
		if !ok {
			return
		}
		fields["description"] = s
	}
	if req.VideoURL != nil {
		s, ok := checkEntryURL(w, *req.VideoURL, "video_url")
		if !ok {
			return
		}
		fields["video_url"] = s
	}
	if req.CodeURL != nil {
		s, ok := checkEntryURL(w, *req.CodeURL, "code_url")
		if !ok {
			return
		}
		fields["code_url"] = s
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string) error {
		return db.UpsertEventEntryText(ctx, tx, a.event.ID, teamID, a.user.ID, fields)
	})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID)
}

func (h *HackHandler) putEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	if !h.shotWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	data, mime, ok := readEntryImage(w, r)
	if !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string) error {
		return db.UpsertEventEntryScreenshot(ctx, tx, a.event.ID, teamID, a.user.ID, data, mime)
	})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID)
}

func (h *HackHandler) deleteEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !h.entryWrites.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	if _, ok := h.participantTeam(w, r, a); !ok {
		return
	}
	teamID, ok := h.entryWrite(w, r, a, func(ctx context.Context, tx *sql.Tx, teamID string) error {
		return db.UpsertEventEntryScreenshot(ctx, tx, a.event.ID, teamID, a.user.ID, nil, "")
	})
	if !ok {
		return
	}
	h.writeOwnEntry(w, r, a.event, teamID)
}

func (h *HackHandler) getEntryScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	h.serveTeamScreenshot(w, r, team.ID)
}

func (h *HackHandler) getTeamScreenshot(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	team, err := db.GetEventTeamBySlug(r.Context(), h.database, a.event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		log.Printf("hack: team screenshot %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	h.serveTeamScreenshot(w, r, team.ID)
}

// listEntries is every team of the event with its entry, for organisers and
// judges. Teams whose deadline has passed are pinned first, so the deadline
// version in the answer is the one that was live when the deadline passed.
func (h *HackHandler) listEntries(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	tl, err := h.loadTeamList(r.Context(), a.event)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	sort.Slice(teams, func(i, j int) bool {
		return strings.ToLower(teams[i].Name) < strings.ToLower(teams[j].Name)
	})
	stored, err := db.ListEventEntries(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: entries %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	byTeam := map[string]db.EventEntry{}
	for _, e := range stored {
		byTeam[e.TeamID] = e
	}
	required := entryRequiredList(a.event.EntryRequired)
	out := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		item, err := h.entryListItem(r.Context(), a.event, team, byTeam[team.ID], required, tl)
		if err != nil {
			log.Printf("hack: entries %s/%s: %v", a.event.Slug, team.Slug, err)
			writeInternal(w)
			return
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"required": required, "entries": out})
}

// entryWrite runs a change to the caller's entry in one transaction. Inside
// it the membership is read again FOR SHARE (a move or removal that committed
// meanwhile wins), and the team row is held FOR SHARE so a deadline pin waits
// until this write commits. ok is false after the answer is written.
func (h *HackHandler) entryWrite(w http.ResponseWriter, r *http.Request, a hackAccess, fn func(ctx context.Context, tx *sql.Tx, teamID string) error) (string, bool) {
	ctx := r.Context()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		writeInternal(w)
		return "", false
	}
	defer tx.Rollback()
	var teamID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT team_id FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND role = 'participant'
		 FOR SHARE`, a.event.ID, a.user.ID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return "", false
	}
	if err != nil {
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	if !teamID.Valid || teamID.String == "" {
		writeHackErr(w, http.StatusConflict, "no_team", "join or start a team first")
		return "", false
	}
	st, err := db.TeamWriteStateFor(ctx, tx, teamID.String, true)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusConflict, "no_team", "join or start a team first")
		return "", false
	}
	if err != nil {
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	if code := st.WriteRefusal(); code != "" {
		status, msg := auth.TeamRefusal(code)
		writeHackErr(w, status, code, msg)
		return "", false
	}
	if err := fn(ctx, tx, teamID.String); err != nil {
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: entry write %s: %v", a.event.Slug, err)
		writeInternal(w)
		return "", false
	}
	return teamID.String, true
}

func (h *HackHandler) writeOwnEntry(w http.ResponseWriter, r *http.Request, ev db.Event, teamID string) {
	body, err := h.ownEntryJSON(r.Context(), ev, teamID)
	if err != nil {
		log.Printf("hack: entry %s: %v", ev.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// ownEntryJSON is the participant's view of their team's entry. editable and
// refusal come from the team's write state, read outside the write transaction.
func (h *HackHandler) ownEntryJSON(ctx context.Context, ev db.Event, teamID string) (map[string]any, error) {
	e, err := db.GetEventEntry(ctx, h.database, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		e = db.EventEntry{}
	} else if err != nil {
		return nil, err
	}
	required := entryRequiredList(ev.EntryRequired)
	complete, missing := entryCompleteness(required, e)
	st, err := db.TeamWriteStateFor(ctx, h.database, teamID, false)
	if err != nil {
		return nil, err
	}
	code := st.WriteRefusal()
	var refusal any
	if code != "" {
		refusal = code
	}
	var updated any
	if e.Exists {
		updated = rfc3339Time(e.UpdatedAt)
	}
	return map[string]any{
		"entry": map[string]any{
			"title":          e.Title,
			"tagline":        e.Tagline,
			"description":    e.Description,
			"video_url":      e.VideoURL,
			"code_url":       e.CodeURL,
			"has_screenshot": e.HasScreenshot,
			"updated_at":     updated,
			"updated_by":     e.UpdatedByName,
		},
		"required": required,
		"complete": complete,
		"missing":  missing,
		"editable": code == "",
		"refusal":  refusal,
		"deadline": rfc3339UTC(st.Deadline),
	}, nil
}

func (h *HackHandler) entryListItem(ctx context.Context, ev db.Event, team db.EventTeam, e db.EventEntry, required []string, tl teamList) (map[string]any, error) {
	complete, missing := entryCompleteness(required, e)
	st := tl.states[team.ID]
	live := tl.site(team.Slug) != nil
	var updated any
	if e.Exists {
		updated = rfc3339Time(e.UpdatedAt)
	}
	var pinned any
	var pinnedURL any
	if st.PinnedVersion.Valid {
		pinned = st.PinnedVersion.Int64
		if u, ok := h.pinnedLink(ev, team, tl, int(st.PinnedVersion.Int64)); ok {
			pinnedURL = u
		}
	}
	return map[string]any{
		"team":            map[string]any{"slug": team.Slug, "name": team.Name},
		"title":           e.Title,
		"tagline":         e.Tagline,
		"description":     e.Description,
		"video_url":       e.VideoURL,
		"code_url":        e.CodeURL,
		"has_screenshot":  e.HasScreenshot,
		"complete":        complete,
		"missing":         missing,
		"updated_at":      updated,
		"site_url":        h.teamSiteURL(ev.Slug, team.Slug),
		"site_exists":     live,
		"site_taken_down": st.SiteTakenDown || st.EventTakenDown,
		"deadline":        rfc3339UTC(st.Deadline),
		"frozen":          st.Frozen(),
		"pinned_version":  pinned,
		"pinned_url":      pinnedURL,
	}, nil
}

// entryCompleteness reports whether every required field is filled. missing
// keeps the canonical field order. A screenshot counts as present when one
// is stored.
func entryCompleteness(required []string, e db.EventEntry) (bool, []string) {
	want := map[string]bool{}
	for _, f := range required {
		want[f] = true
	}
	missing := []string{}
	for _, field := range hackEntryFields {
		if want[field] && !entryFieldPresent(field, e) {
			missing = append(missing, field)
		}
	}
	return len(missing) == 0, missing
}

func entryFieldPresent(field string, e db.EventEntry) bool {
	switch field {
	case "title":
		return e.Title != ""
	case "tagline":
		return e.Tagline != ""
	case "description":
		return e.Description != ""
	case "video_url":
		return e.VideoURL != ""
	case "code_url":
		return e.CodeURL != ""
	case "screenshot":
		return e.HasScreenshot
	default:
		return false
	}
}

// checkEntryURL accepts "" or an absolute http or https URL with a host, at
// most 500 characters, with no whitespace or control characters.
func checkEntryURL(w http.ResponseWriter, s, field string) (string, bool) {
	if s == "" {
		return "", true
	}
	if utf8.RuneCountInString(s) > 500 || urlHasSpaceOrControl(s) || !absoluteHTTPURL(s) {
		writeHackErr(w, http.StatusBadRequest, "invalid_url", field+" must be an absolute http or https URL of at most 500 characters")
		return "", false
	}
	return s, true
}

func urlHasSpaceOrControl(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func absoluteHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
		return false
	}
	return u.Host != ""
}

// readEntryImage reads a screenshot body. The limit is 2 MiB plus one byte
// so a body just over the limit is still this handler's 413, not a cut-off
// read. The type comes only from the magic bytes: Content-Type is ignored,
// and SVG and GIF are not images we store.
func readEntryImage(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, hackScreenshotMax+1)
	data, err := io.ReadAll(r.Body)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) || len(data) > hackScreenshotMax {
		writeHackErr(w, http.StatusRequestEntityTooLarge, "image_too_large", "the screenshot must be 2 MB or smaller")
		return nil, "", false
	}
	if err != nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "could not read the screenshot")
		return nil, "", false
	}
	mime, ok := entryImageType(data)
	if !ok {
		writeHackErr(w, http.StatusUnsupportedMediaType, "unsupported_image", "the screenshot must be a PNG, JPEG or WebP image")
		return nil, "", false
	}
	// A small file can declare an enormous picture; refuse one no screen
	// needs.
	wd, ht, ok := 0, 0, false
	if mime == "image/webp" {
		wd, ht, ok = webpSize(data)
	} else if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		wd, ht, ok = cfg.Width, cfg.Height, true
	}
	if !ok || wd < 1 || ht < 1 || wd > hackScreenshotMaxSide || ht > hackScreenshotMaxSide {
		writeHackErr(w, http.StatusUnsupportedMediaType, "unsupported_image", "the screenshot must be a PNG, JPEG or WebP image at most 8000 pixels on a side")
		return nil, "", false
	}
	return data, mime, true
}

func entryImageType(b []byte) (string, bool) {
	if len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return "image/png", true
	}
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return "image/jpeg", true
	}
	if len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")) {
		return "image/webp", true
	}
	return "", false
}

func entryImageAllowed(mime string) bool {
	switch mime {
	case "image/png", "image/jpeg", "image/webp":
		return true
	default:
		return false
	}
}

func (h *HackHandler) serveTeamScreenshot(w http.ResponseWriter, r *http.Request, teamID string) {
	data, mime, err := db.GetEventScreenshot(r.Context(), h.database, teamID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeHackErr(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
			return
		}
		log.Printf("hack: screenshot: %v", err)
		writeInternal(w)
		return
	}
	if len(data) == 0 || !entryImageAllowed(mime) {
		writeHackErr(w, http.StatusNotFound, "no_screenshot", "this team has no screenshot")
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", "inline")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// webpSize reads a WebP's canvas size from its first chunk: VP8X (extended),
// VP8L (lossless) or VP8 (lossy). ok=false for anything else.
func webpSize(b []byte) (w, h int, ok bool) {
	if len(b) < 30 {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8X":
		w = 1 + (int(b[24]) | int(b[25])<<8 | int(b[26])<<16)
		h = 1 + (int(b[27]) | int(b[28])<<8 | int(b[29])<<16)
		return w, h, true
	case "VP8L":
		if b[20] != 0x2f {
			return 0, 0, false
		}
		v := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		return int(v&0x3fff) + 1, int(v>>14&0x3fff) + 1, true
	case "VP8 ":
		if b[23] != 0x9d || b[24] != 0x01 || b[25] != 0x2a {
			return 0, 0, false
		}
		return int(uint16(b[26])|uint16(b[27])<<8) & 0x3fff, int(uint16(b[28])|uint16(b[29])<<8) & 0x3fff, true
	}
	return 0, 0, false
}
