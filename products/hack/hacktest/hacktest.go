package hacktest

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	accountrepo "github.com/vsriram/simple-host/host/access/repo"
	platformtestdb "github.com/vsriram/simple-host/host/testdb"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	"github.com/vsriram/simple-host/products/hack/core"
	productedition "github.com/vsriram/simple-host/products/hack/edition"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
)

func WantTS(t *testing.T, what string, r Resp, status int, code string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: got %d want %d: %s", what, r.Status, status, r.Body)
	}
	if code != "" {
		if got, _ := r.JSON(t)["code"].(string); got != code {
			t.Fatalf("%s: code %q want %q: %s", what, got, code, r.Body)
		}
	}
}

func (m *ReplyMailer) Messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.Sent...)
}

func (a *App) CountQuery(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := a.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type MailSink struct {
	Notices []Notice
}

// Notice is one email a MailSink received.
type Notice struct{ To, Subject, Body, HTML string }

func (*MailSink) SendSignInCode(string, string, string) error { return nil }

func (s *MailSink) SendNotice(to, subject, body string) error {
	s.Notices = append(s.Notices, Notice{to, subject, body, ""})
	return nil
}

func (s *MailSink) SendRichNoticeCtx(_ context.Context, to, subject, body, html string) error {
	s.Notices = append(s.Notices, Notice{to, subject, body, html})
	return nil
}

// titleOnlyEntryFields is every entry-rules field but title set to
// optional, for tests that need a minimal rule set (the default preset
// otherwise requires more than a title before an entry can be submitted)
// but don't exercise the rules engine itself. Sent as "fields" with no
// "preset": the server demotes the stored preset to custom on its own, so
// these tests never send the now-refused preset value "custom" directly
// (SIM-382: only video_or_website and video_website_code may be set that way).
var TitleOnlyEntryFields = map[string]string{
	"problem": "optional", "tagline": "optional", "audience": "optional", "track": "optional",
	"tools": "optional", "maturity": "optional", "description": "optional",
	"video_url": "optional", "site": "optional", "code_url": "optional", "screenshot": "optional",
}

type EntryWorld struct {
	A     *App
	Slug  string
	Org   Person
	P1    Person // team Alpha
	P2    Person // team zeta
	Lone  Person // participant on no team
	Judge Person
	Alpha string
	Zeta  string
}

// NewEntryWorld makes an open event with title-only entry rules, an organiser, a judge, two teams (zeta made first, then Alpha) and a participant on none.
func NewEntryWorld(t *testing.T, a *App) *EntryWorld {
	t.Helper()
	org := a.NewPerson(t, "eo")
	p1 := a.NewPerson(t, "e1")
	p2 := a.NewPerson(t, "e2")
	lone := a.NewPerson(t, "e0")
	judge := a.NewPerson(t, "ej")
	slug := UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	join, jcode := a.JoinCodes(t, org, slug)
	joinAs := func(p Person, name string, asJudge bool) {
		t.Helper()
		if asJudge {
			a.JoinJudge(t, jcode, p, name)
		} else {
			a.JoinParticipant(t, join, p, name)
		}
	}
	// Entry rules: slice 1's default "video_or_website" preset requires more than a title
	// (problem, tagline, audience, track, tools, maturity, plus a video or
	// site). This world's tests exercise the title/tagline/etc fields and
	// the submissions-closed flow, not the rules engine itself, so reset to
	// a minimal rule set via titleOnlyEntryFields (title only, same as the
	// old entry_required default) to keep their completeness assertions
	// meaningful.
	a.PutEntryRules(t, slug, TitleOnlyEntryFields)
	joinAs(p1, "Ada One", false)
	joinAs(p2, "Bea Two", false)
	joinAs(lone, "Lonely", false)
	joinAs(judge, "Jude", true)
	// zeta is created first so name order is not creation order.
	zeta := a.CreateTeam(t, slug, p2, "zeta").Slug
	alpha := a.CreateTeam(t, slug, p1, "Alpha").Slug
	return &EntryWorld{a, slug, org, p1, p2, lone, judge, alpha, zeta}
}

func (a *App) RawBody(t *testing.T, method, path string, body []byte, contentType string, headers map[string]string) Resp {
	t.Helper()
	req, err := http.NewRequest(method, a.Srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return Resp{res.StatusCode, res.Header, b}
}

func (w *EntryWorld) EntryPath() string {
	return "/v1/hack/events/" + w.Slug + "/entry"
}

func JSONStrings(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("want array, got %#v", v)
	}
	out := make([]string, len(raw))
	for i, item := range raw {
		s, ok := item.(string)
		if !ok {
			t.Fatalf("want string, got %#v", item)
		}
		out[i] = s
	}
	return out
}

// entrySiteStub is the site side the entries list needs for a deadline
// preview link. The real site handler is not wired in these tests.
type SiteStub struct{}

func (SiteStub) TeamSitesReady(string) bool { return true }

func (SiteStub) TeamSiteURL(eventSlug, teamSlug string) string {
	return "https://" + teamSlug + "." + eventSlug + ".simple-hack.test/"
}

func (SiteStub) TeamSiteInfo(context.Context, string, string) (model.TeamSite, model.Site, error) {
	return model.TeamSite{}, model.Site{}, nil
}

func (SiteStub) TeamPreviewLink(_ context.Context, _, eventSlug, teamSlug string, n int) (string, time.Time, bool) {
	return "https://preview.example/" + eventSlug + "/" + teamSlug + "/v" + strconv.Itoa(n), time.Time{}, true
}

func (SiteStub) SuspendHeldSite(context.Context, sites.HeldSiteSuspension) error {
	return nil
}

func (SiteStub) TrashTeamSite(context.Context, string, string) error { return nil }

func (SiteStub) RequestSiteCert(string) {}

func (SiteStub) TeamPreviewLinkFor(site model.Site, eventSlug string, n int) (string, bool) {
	return "https://preview.example/" + eventSlug + "/" + site.Name + "/v" + strconv.Itoa(n), true
}

func (SiteStub) RemoveAccountFiles(string, string) error { return nil }

func (SiteStub) SyncAccountMarkers(context.Context, string) error { return nil }

func (SiteStub) TeamSiteFlatHostsOn() bool { return false }

// tinyJPEG is a real 2x2 JPEG (DecodeConfig reads its header).
func TinyJPEG() []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil)
	return b.Bytes()
}

type RulesTeam struct {
	Person Person
	Slug   string
}

type RulesWorld struct {
	A     *App
	Slug  string
	Org   Person
	Judge Person
	Teams []RulesTeam
}

// NewRulesWorld makes an event (default video_or_website preset, no tracks), an
// organiser, a judge, and nTeams participants each already on their own
// solo team.
func NewRulesWorld(t *testing.T, a *App, nTeams int) *RulesWorld {
	t.Helper()
	org := a.NewPerson(t, "ruo")
	judge := a.NewPerson(t, "ruj")
	slug := UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	join, jcode := a.JoinCodes(t, org, slug)
	a.JoinJudge(t, jcode, judge, "Jude")
	w := &RulesWorld{A: a, Slug: slug, Org: org, Judge: judge}
	for i := 0; i < nTeams; i++ {
		p := a.NewPerson(t, "rup")
		name := fmt.Sprintf("Rules Team %c", 'A'+i)
		a.JoinParticipant(t, join, p, name)
		w.Teams = append(w.Teams, RulesTeam{Person: p, Slug: a.CreateTeam(t, slug, p, name).Slug})
	}
	return w
}

func (w *RulesWorld) EntryPath() string { return "/v1/hack/events/" + w.Slug + "/entry" }

func (w *RulesWorld) TeamID(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := w.A.DB.QueryRow(`
		SELECT id FROM event_teams WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, w.Slug, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// buildCompleteFields are the fields that satisfy the default "video_or_website"
// preset with no event tracks: everything required, plus video_url for the
// one_of group.
func CompleteFields() map[string]any {
	return map[string]any{
		"title": "A Title", "problem": "A problem", "tagline": "A tagline", "audience": "Everyone",
		"tools": []string{"Go"}, "maturity": "working", "video_url": "https://example.com/v",
	}
}

// liveSiteStub is entrySiteStub with the team sites named in live reported
// as live (a published version), so the settle hook sees them.
type LiveSiteStub struct {
	SiteStub
	Live map[string]bool
}

func (s LiveSiteStub) TeamSiteInfo(_ context.Context, _ string, teamSlug string) (model.TeamSite, model.Site, error) {
	if s.Live[teamSlug] {
		return model.TeamSite{Exists: true, ActiveVersion: 1}, model.Site{Name: teamSlug, ActiveVersion: 1}, nil
	}
	return model.TeamSite{}, model.Site{}, nil
}

type JudgingPerson struct {
	Person
	ID, Name string
}

type JudgingTeam struct {
	ID, Name string
}

type JudgingWorld struct {
	A        *App
	Slug     string
	Org      Person
	Outsider Person
	Part     JudgingPerson
	Judges   []JudgingPerson
	Teams    []JudgingTeam
}

// NewJudgingWorld makes a closed event with nTeams teams (each with a submitted entry) and nJudges judges.
func NewJudgingWorld(t *testing.T, a *App, nTeams, nJudges int) *JudgingWorld {
	t.Helper()
	org := a.NewPerson(t, "jg")
	outsider := a.NewPerson(t, "jx")
	slug := UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	a.OpenEvent(t, org, slug)
	join, jcode := a.JoinCodes(t, org, slug)

	w := &JudgingWorld{A: a, Slug: slug, Org: org, Outsider: outsider}
	joinAs := func(p Person, name string, asJudge bool) {
		t.Helper()
		if asJudge {
			a.JoinJudge(t, jcode, p, name)
		} else {
			a.JoinParticipant(t, join, p, name)
		}
	}

	nParts := nTeams
	if nParts < 1 {
		nParts = 1
	}
	parts := make([]Person, nParts)
	for i := range parts {
		parts[i] = a.NewPerson(t, "jp")
		name := fmt.Sprintf("Pat %d", i+1)
		joinAs(parts[i], name, false)
		if i == 0 {
			w.Part = JudgingPerson{Person: parts[i], ID: a.UserID(t, parts[i]), Name: name}
		}
	}
	for i := 0; i < nTeams; i++ {
		name := fmt.Sprintf("Team %c", 'A'+i)
		id := a.CreateTeam(t, slug, parts[i], name).ID
		w.Teams = append(w.Teams, JudgingTeam{ID: id, Name: name})
		// A submitted entry: the judge queue and eligibility (section 4.5,
		// zv-hack-entry-summary.sql) only ever show a team whose entry has
		// been decided one way. This test exercises assignment modes, not
		// entry completeness, so the entry is written directly rather than
		// threaded through the rules engine.
		eventID := a.DB.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug)
		var evID string
		if err := eventID.Scan(&evID); err != nil {
			t.Fatal(err)
		}
		if _, err := a.DB.Exec(`
			INSERT INTO event_entries (team_id, event_id, title, submitted_at, submit_settled_at)
			VALUES ($1, $2, $3, now(), now())
			ON CONFLICT (team_id) DO UPDATE SET submitted_at = now(), submit_settled_at = now()`,
			id, evID, name); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < nJudges; i++ {
		p := a.NewPerson(t, "jj")
		name := fmt.Sprintf("Judge %d", i+1)
		joinAs(p, name, true)
		w.Judges = append(w.Judges, JudgingPerson{Person: p, ID: a.UserID(t, p), Name: name})
	}
	if _, err := a.DB.Exec(`UPDATE events SET stage='closed' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	return w
}

func (a *App) TeamIDByName(t *testing.T, slug, name string) string {
	t.Helper()
	var id string
	err := a.DB.QueryRow(`
		SELECT t.id FROM event_teams t
		JOIN events e ON e.id = t.event_id
		WHERE e.slug = $1 AND t.name = $2`, slug, name).Scan(&id)
	if err != nil {
		t.Fatalf("team id %s: %v", name, err)
	}
	return id
}

func (a *App) EmailOf(t *testing.T, p Person) string {
	t.Helper()
	return p.Email
}

func (a *App) EventID(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := a.DB.QueryRow(`SELECT id FROM events WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *App) IsMember(t *testing.T, slug, userID string) bool {
	t.Helper()
	var n int
	if err := a.DB.QueryRow(`SELECT count(*) FROM event_members WHERE event_id = (SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func (a *App) FirstCriterionID(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := a.DB.QueryRow(`SELECT id FROM rubric_criteria WHERE event_id = $1 ORDER BY position LIMIT 1`, a.EventID(t, slug)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *App) TeamSlugByID(t *testing.T, teamID string) string {
	t.Helper()
	var slug string
	if err := a.DB.QueryRow(`SELECT slug FROM event_teams WHERE id = $1`, teamID).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	return slug
}

func (a *App) TeamCode(t *testing.T, teamID string) string {
	t.Helper()
	var code string
	if err := a.DB.QueryRow(`SELECT code FROM event_teams WHERE id = $1`, teamID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	return code
}

func (a *App) IsTeamAdmin(t *testing.T, p Person) bool {
	t.Helper()
	var admin bool
	if err := a.DB.QueryRow(`SELECT team_admin FROM event_members WHERE user_id = $1`, a.UserID(t, p)).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	return admin
}

func (a *App) TeamOf(t *testing.T, p Person) string {
	t.Helper()
	var team sql.NullString
	if err := a.DB.QueryRow(`SELECT team_id FROM event_members WHERE user_id = $1`, a.UserID(t, p)).Scan(&team); err != nil {
		t.Fatal(err)
	}
	return team.String
}

// person and resp (cleanup 3.2 slice 3): duplicated from
// host/handler/connector_test.go rather than reused — a _test.go
// file's symbols, even exported ones, are never visible to another
// package, so these two tiny generic HTTP-test types get their own copy
// here, the same way hack_entries.go's own hackApp.newKeyOnlyPerson already
// duplicates part of connectorApp.newPerson's body.
type Person struct{ Email, Key string }

type Resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r Resp) JSON(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("not JSON (%d): %s", r.Status, r.Body)
	}
	return out
}

func JSONBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

// hackHandleCheckerForTest uses the app service that owns the handle namespace.
func hackHandleCheckerForTest(ctx context.Context, q sqlkit.Querier, h string) (events.HandleVerdict, error) {
	v, err := accountapp.JudgeHandle(ctx, q, "", h)
	return events.HandleVerdict{Status: v.Status, Code: v.Code}, err
}

// replyMailer is an email.Sender that also records SendNoticeReplyTo calls.
// Duplicated from host/handler/idle_test.go (cleanup 3.2 slice 3):
// hack_cleanup_test.go and hack_rehearsal_test.go both moved with it, and
// it has no handler-private dependency.
type ReplyMailer struct {
	mu   sync.Mutex
	Sent []string // to|replyTo|subject|text
}

func (m *ReplyMailer) SendSignInCode(string, string, string) error { return nil }

func (m *ReplyMailer) SendNoticeReplyTo(to, replyTo, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sent = append(m.Sent, to+"|"+replyTo+"|"+subject+"|"+text)
	return nil
}

// failingReplyMailer is idle_test.go's own (host/handler), duplicated
// (cleanup 3.2 slice 3): hack_cleanup_test.go, its only mover, needs it.
type FailingReplyMailer struct{ ReplyMailer }

func (m *FailingReplyMailer) SendNoticeReplyTo(to, replyTo, subject, text string) error {
	return errors.New("mail server down")
}

func (a *App) At(t *testing.T, method, path string, body any, headers map[string]string) Resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			rd = JSONBody(body)
		}
	}
	req, err := http.NewRequest(method, a.Srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return Resp{res.StatusCode, res.Header, b}
}

func (a *App) Key(p Person) map[string]string {
	return map[string]string{"X-API-Key": p.Key}
}

func (a *App) AdminH() map[string]string {
	return map[string]string{"X-API-Key": a.Admin}
}

func (a *App) NewPerson(t *testing.T, label string) Person {
	t.Helper()
	addr := label + "-" + strings.ReplaceAll(UniqueSlug(), "-", "") + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := accountrepo.CreateUser(context.Background(), a.DB, addr, key, false)
	if err != nil {
		t.Fatal(err)
	}
	handle := "u" + UniqueSlug()
	if _, err := a.DB.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, u.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.DB.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	a.ids[key] = u.ID
	return Person{Email: addr, Key: key}
}

func (a *App) UserID(t *testing.T, p Person) string {
	t.Helper()
	var id string
	if err := a.DB.QueryRow(`SELECT id FROM users WHERE username = $1`, p.Email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *App) CleanupEvent(slug string) {
	var accountID string
	_ = a.DB.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
	_, _ = a.DB.Exec(`DELETE FROM events WHERE slug = $1`, slug)
	if accountID != "" {
		_, _ = a.DB.Exec(`DELETE FROM users WHERE id = $1`, accountID)
	}
}

// wantCode is kinds_test.go's own (host/handler), duplicated (cleanup
// 3.2 slice 3): no handler-private dependency once resp/person are here.
func WantCode(t *testing.T, what string, r Resp, status int, code string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: %d %s, want %d %s", what, r.Status, r.Body, status, code)
	}
	if code != "" {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(r.Body, &body)
		if body.Code != code {
			t.Fatalf("%s: code %q (%s), want %q", what, body.Code, r.Body, code)
		}
	}
}

// waitBlocked is review_fixes_test.go's own (host/handler), duplicated
// (cleanup 3.2 slice 3): a one-line sleep, no handler-private dependency.
func WaitBlocked() { time.Sleep(300 * time.Millisecond) }

func UniqueSlug() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "hax" + hex.EncodeToString(b[:])
}

// setHackAppLimits overwrites a's already-built HackHandler's own limits
// snapshot in place (moved from host/handler/limits_test.go, cleanup
// 3.2 slice 3: hackApp, its only caller, moved here; limits_test.go's
// setAppLimits/setConnectorAppLimits stay there for the fixtures that stay
// there). See that file's withLimits/otherValue for the rest of the
// env-knob test machinery this pairs with.
func SetLimits(a *App, l productconfig.Limits) {
	a.Hack.Limits.Limits = l.Limits
	a.Hack.SetProductLimits(l)
}

func (a *App) CreateBody(slug string, extra map[string]any) map[string]any {
	body := map[string]any{
		"slug":                  slug,
		"title":                 "Spring hack",
		"organiser_name":        "Ada Lovelace",
		"organisation":          "Analytic Club",
		"contact_email":         "ada@example.com",
		"purpose":               "build something kind",
		"expected_participants": 80,
		"starts_at":             "2026-10-01",
		"ends_at":               "2026-10-03",
		"time_zone":             "UTC",
	}
	for k, v := range extra {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	return body
}

// giveSubmittedEntry writes (or updates) a minimal, already-decided entry
// for teamID directly via SQL: submitted_at and submit_settled_at both set.
// Entries and participant journey slice 1 (zv-hack-entry-summary.sql) made
// judging eligibility depend on EntryJudgeableSQL -- a submitted or settled
// entry, not excluded -- so a team with no entry row at all is never
// judgeable. Many tests outside the entries package score a team directly
// to exercise leave/removal/judging behaviour rather than the entries API
// itself; this is their fixture, the same direct INSERT newJudgingWorld
// uses (hack_judging_test.go). It never touches title/tagline/etc, so it is
// safe to call after an entry already exists (e.g. via PUT .../entry).
func (a *App) GiveSubmittedEntry(t *testing.T, slug, teamID string) {
	t.Helper()
	if _, err := a.DB.Exec(`
		INSERT INTO event_entries (team_id, event_id, submitted_at, submit_settled_at)
		VALUES ($1, (SELECT id FROM events WHERE slug = $2), now(), now())
		ON CONFLICT (team_id) DO UPDATE SET submitted_at = now(), submit_settled_at = now()`,
		teamID, slug); err != nil {
		t.Fatal(err)
	}
}

func JSONArr(t *testing.T, r Resp) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("not array (%d): %s", r.Status, r.Body)
	}
	return out
}

// pngOfSize is a PNG header declaring w x h (enough for DecodeConfig).
func PNGOfSize(w, h int) []byte {
	ihdr := []byte{'I', 'H', 'D', 'R', byte(w >> 24), byte(w >> 16), byte(w >> 8), byte(w), byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h), 8, 2, 0, 0, 0}
	c := crc32.ChecksumIEEE(ihdr)
	b := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x0d")
	b = append(b, ihdr...)
	return append(b, byte(c>>24), byte(c>>16), byte(c>>8), byte(c))
}

// App is one test server: the Hack core on a disposable database, with the
// routes a test package registers on Mux.
type App struct {
	DB     *sql.DB
	Admin  string // the admin's API key
	Mux    *http.ServeMux
	Srv    *httptest.Server
	Hack   *core.Hack
	AuthMW func(http.Handler) http.Handler

	adminID string
	ids     map[string]string // API key -> user id, for the setup calls
}

// New builds the core for one test; the caller registers its feature routes on a.Mux.
func New(t *testing.T, eds ...*edition.Edition) *App {
	t.Helper()
	accountapp.SetGroupHooks(accountapp.GroupHooks{Erasure: members.AccountErasure{}})
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	ed := productedition.ForKind(productedition.Hack)
	if len(eds) > 0 {
		ed = eds[0]
	}
	database := platformtestdb.DB(t)
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := accountrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	h := core.New(database, srv.URL, "simple-hack.test", ed)
	h.Events.SetHandleChecker(hackHandleCheckerForTest)
	return &App{DB: database, Admin: adminKey, Mux: mux, Srv: srv, Hack: h,
		AuthMW:  auth.Middleware(adminKey, adminID, accountapp.New(database), ed.Limits().KeyIdleExpiry),
		adminID: adminID, ids: map[string]string{}}
}

// setStage moves the event straight to stage, as the world's setup does.
func (w *JudgingWorld) SetStage(t *testing.T, stage string) {
	t.Helper()
	if _, err := w.A.DB.Exec(`UPDATE events SET stage=$2 WHERE slug=$1`, w.Slug, stage); err != nil {
		t.Fatal(err)
	}
}

func (w *JudgingWorld) Path(rest string) string {
	return "/v1/hack/events/" + w.Slug + rest
}

func (w *JudgingWorld) Call(t *testing.T, method, rest string, body any, p Person) Resp {
	t.Helper()
	return w.A.At(t, method, w.Path(rest), body, w.A.Key(p))
}

func (w *JudgingWorld) CountRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := w.A.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func Criterion(name, desc string, weight, points int) map[string]any {
	return map[string]any{"name": name, "description": desc, "weight": weight, "max_points": points}
}

func RubricBody(items ...map[string]any) map[string]any {
	return map[string]any{"criteria": items}
}

// scoreTeam is a small helper: PUT scores for one (judge, team).
func (w *JudgingWorld) ScoreTeam(t *testing.T, judge JudgingPerson, teamID string, scores map[string]int, comment *string) Resp {
	t.Helper()
	body := map[string]any{}
	list := make([]map[string]any, 0, len(scores))
	for cid, pts := range scores {
		list = append(list, map[string]any{"criterion_id": cid, "points": pts})
	}
	body["scores"] = list
	if comment != nil {
		body["comment"] = *comment
	}
	return w.Call(t, "PUT", "/judge/scores/"+teamID, body, judge.Person)
}

type RubricCriterion struct {
	ID          string `json:"id"`
	Position    int    `json:"position"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
	MaxPoints   int    `json:"max_points"`
}

type RubricView struct {
	Criteria []RubricCriterion `json:"criteria"`
}

func DecodeRubric(t *testing.T, r Resp) RubricView {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("rubric: %d %s", r.Status, r.Body)
	}
	var v RubricView
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatalf("rubric json: %v body %s", err, r.Body)
	}
	return v
}

// listEvent lists the event in Find a hackathon. Applications are taken only
// from listed events (owner, 2026-10-31).
func (a *App) ListEvent(t *testing.T, slug string) {
	t.Helper()
	if _, err := a.DB.Exec(`UPDATE events SET directory_listed=true WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
}

func ErasureBlockForTest(t *testing.T, ctx context.Context, db *sql.DB, id string) (string, string, error) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	held, err := accountapp.HoldingAccount(ctx, tx, id)
	if err != nil {
		return "", "", err
	}
	if held {
		return "event_account", accountapp.HoldingText("delete"), nil
	}
	code, msg, err := (members.AccountErasure{}).Block(ctx, tx, id, false)
	if err == nil && code == "" {
		err = tx.Commit()
	}
	return code, msg, err
}

// applyBase helpers build a valid POST .../apply body, letting the caller
// override individual fields.
func ApplyBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"accept_coc":   true,
		"display_name": "Dewi Lestari",
		"city":         "Jakarta",
		"country":      "ID",
		"age_range":    "under_18",
		"gender":       "woman",
		"answers":      map[string]any{},
	}
	for k, v := range extra {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	return body
}

func (a *App) Apply(t *testing.T, slug string, p Person, extra map[string]any) Resp {
	t.Helper()
	return a.At(t, "POST", "/v1/hack/events/"+slug+"/apply", ApplyBody(extra), a.Key(p))
}

func (a *App) ApplyGet(t *testing.T, slug string, p *Person) Resp {
	t.Helper()
	headers := map[string]string{}
	if p != nil {
		headers = a.Key(*p)
	}
	return a.At(t, "GET", "/v1/hack/events/"+slug+"/apply", nil, headers)
}

func (a *App) Leave(t *testing.T, slug string, p Person, body map[string]any) Resp {
	t.Helper()
	return a.At(t, "POST", "/v1/hack/events/"+slug+"/leave", body, a.Key(p))
}

func (a *App) PreviewLeave(t *testing.T, slug string, p Person) Resp {
	t.Helper()
	return a.At(t, "GET", "/v1/hack/events/"+slug+"/leave", nil, a.Key(p))
}

func (a *App) EventAccount(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := a.DB.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
