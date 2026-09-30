package handler

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
)

type hackApp struct {
	database *sql.DB
	admin    string
	mux      *http.ServeMux
	srv      *httptest.Server
	hack     *HackHandler
}

func newHackApp(t *testing.T) *hackApp {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := db.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var root http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	authMW := auth.Middleware(adminKey, adminID, database)
	users := NewUserHandler(database, email.NewResendSender("", "test@example.com"), srv.URL)
	users.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	h := NewHackHandler(database, srv.URL, "simple-hack.test")
	h.Register(mux, authMW)
	root = mux
	return &hackApp{database: database, admin: adminKey, mux: mux, srv: srv, hack: h}
}

func (a *hackApp) at(t *testing.T, method, path string, body any, headers map[string]string) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			rd = jsonBody(body)
		}
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rd)
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
	return resp{res.StatusCode, res.Header, b}
}

func (a *hackApp) key(p person) map[string]string {
	return map[string]string{"X-API-Key": p.key}
}

func (a *hackApp) adminH() map[string]string {
	return map[string]string{"X-API-Key": a.admin}
}

func (a *hackApp) newPerson(t *testing.T, label string) person {
	t.Helper()
	addr := label + "-" + strings.ReplaceAll(uniqueSlug(), "-", "") + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := db.CreateUser(context.Background(), a.database, addr, key, false)
	if err != nil {
		t.Fatal(err)
	}
	handle := "u" + uniqueSlug()
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, u.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	return person{email: addr, key: key}
}

func (a *hackApp) userID(t *testing.T, p person) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *hackApp) cleanupEvent(slug string) {
	var accountID string
	_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
	_, _ = a.database.Exec(`DELETE FROM events WHERE slug = $1`, slug)
	if accountID != "" {
		_, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, accountID)
	}
}

func uniqueSlug() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "hax" + hex.EncodeToString(b[:])
}

func (a *hackApp) createBody(slug string, extra map[string]any) map[string]any {
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

func (a *hackApp) createEvent(t *testing.T, p person, slug string, extra map[string]any) resp {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/events", a.createBody(slug, extra), a.key(p))
	if r.status == http.StatusCreated {
		t.Cleanup(func() { a.cleanupEvent(slug) })
	}
	return r
}

func (a *hackApp) openEvent(t *testing.T, p person, slug string) {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "open"}, a.key(p))
	if r.status != 200 {
		t.Fatalf("open: %d %s", r.status, r.body)
	}
}

func jsonArr(t *testing.T, r resp) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("not array (%d): %s", r.status, r.body)
	}
	return out
}

func TestHackCreateSuccessAndHoldingAccount(t *testing.T) {
	a := newHackApp(t)
	p := a.newPerson(t, "org")
	slug := uniqueSlug()
	r := a.createEvent(t, p, slug, nil)
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	j := r.json(t)
	ev := j["event"].(map[string]any)
	if ev["slug"] != slug || ev["stage"] != "draft" || ev["url"] != "https://"+slug+".simple-hack.test/" {
		t.Fatalf("event: %v", ev)
	}
	if j["role"] != "organiser" {
		t.Fatalf("role: %v", j["role"])
	}
	org := j["organiser"].(map[string]any)
	if org["join_code"] == nil || org["judge_code"] == nil || org["join_url"] == nil {
		t.Fatalf("codes: %v", org)
	}
	var eventAccount bool
	var keys int
	var username string
	err := a.database.QueryRow(`
		SELECT u.event_account, (SELECT COUNT(*) FROM api_keys k WHERE k.user_id = u.id), u.username
		  FROM events e JOIN users u ON u.id = e.account_id WHERE e.slug = $1`, slug).Scan(&eventAccount, &keys, &username)
	if err != nil {
		t.Fatal(err)
	}
	if !eventAccount || keys != 0 {
		t.Fatalf("holding account event_account=%v keys=%d", eventAccount, keys)
	}
	if !strings.HasPrefix(username, "event+") || !strings.HasSuffix(username, "@events.invalid") {
		t.Fatalf("username: %s", username)
	}
}

func TestHackCreateValidation(t *testing.T) {
	a := newHackApp(t)
	p := a.newPerson(t, "val")
	slug := uniqueSlug()
	cases := []struct {
		name   string
		extra  map[string]any
		status int
		code   string
	}{
		{"short", map[string]any{"slug": "ab"}, 400, "invalid_name"},
		{"xn", map[string]any{"slug": "xn--abc"}, 400, "invalid_name"},
		{"hyphen", map[string]any{"slug": "-abc"}, 400, "invalid_name"},
		{"reserved-join", map[string]any{"slug": "join"}, 400, "name_reserved"},
		{"reserved-admin", map[string]any{"slug": "admin"}, 400, "name_reserved"},
		{"reserved-www", map[string]any{"slug": "www"}, 400, "name_reserved"},
		{"empty-title", map[string]any{"title": ""}, 400, "invalid_title"},
		{"long-title", map[string]any{"title": strings.Repeat("t", 121)}, 400, "invalid_title"},
		{"empty-organiser", map[string]any{"organiser_name": ""}, 400, "invalid_organiser_name"},
		{"bad-email", map[string]any{"contact_email": "not-an-email"}, 400, "invalid_contact_email"},
		{"named-email", map[string]any{"contact_email": "Ada <ada@example.com>"}, 400, "invalid_contact_email"},
		{"empty-purpose", map[string]any{"purpose": ""}, 400, "invalid_purpose"},
		{"zero-people", map[string]any{"expected_participants": 0}, 400, "invalid_expected_participants"},
		{"too-many-people", map[string]any{"expected_participants": 100001}, 400, "invalid_expected_participants"},
		{"missing-start", map[string]any{"starts_at": ""}, 400, "invalid_starts_at"},
		{"ends-before", map[string]any{"starts_at": "2026-10-05", "ends_at": "2026-10-01"}, 400, "invalid_ends_at"},
		{"local-tz", map[string]any{"time_zone": "Local"}, 400, "invalid_time_zone"},
		{"bad-tz", map[string]any{"time_zone": "Not/AZone"}, 400, "invalid_time_zone"},
		{"control", map[string]any{"title": "hi\x00there"}, 400, "invalid_title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := a.createBody(slug, tc.extra)
			r := a.at(t, "POST", "/v1/hack/events", body, a.key(p))
			if r.status != tc.status || r.json(t)["code"] != tc.code {
				t.Fatalf("got %d %s want %d %s", r.status, r.body, tc.status, tc.code)
			}
		})
	}
}

func TestHackCreateNameTakenHandlePeerBudgetLimits(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lim")
	other := a.newPerson(t, "hold")

	taken := uniqueSlug()
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE username = $2`, taken, other.email); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "POST", "/v1/hack/events", a.createBody(taken, nil), a.key(org))
	if r.status != http.StatusConflict || r.json(t)["code"] != "name_taken" {
		t.Fatalf("handle taken: %d %s", r.status, r.body)
	}

	a.hack.SetNamePeer(func(ctx context.Context, name string) (bool, error) { return true, nil })
	peerSlug := uniqueSlug()
	r = a.at(t, "POST", "/v1/hack/events", a.createBody(peerSlug, nil), a.key(org))
	if r.status != http.StatusConflict || r.json(t)["code"] != "name_taken" {
		t.Fatalf("peer taken: %d %s", r.status, r.body)
	}

	a.hack.SetNamePeer(func(ctx context.Context, name string) (bool, error) { return false, errors.New("peer down") })
	r = a.at(t, "POST", "/v1/hack/events", a.createBody(uniqueSlug(), nil), a.key(org))
	if r.status != http.StatusServiceUnavailable || r.json(t)["code"] != "name_check_unavailable" {
		t.Fatalf("peer err: %d %s", r.status, r.body)
	}
	check := a.at(t, "GET", "/v1/hack/names/"+uniqueSlug(), nil, a.key(org))
	if check.status != http.StatusServiceUnavailable || check.json(t)["code"] != "name_check_unavailable" {
		t.Fatalf("names peer err: %d %s", check.status, check.body)
	}
	a.hack.SetNamePeer(nil)

	a.hack.SetInstanceUsage(func(ctx context.Context) (int64, error) { return 9 * 1024 * 1024 * 1024, nil })
	old := *config.Active()
	l := old
	l.HackInstanceBudgetGB = 10
	l.EventCreatePerDay = 1
	l.EventMaxActivePerOrganiser = 1
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })
	r = a.at(t, "POST", "/v1/hack/events", a.createBody(uniqueSlug(), nil), a.key(org))
	if r.status != http.StatusInsufficientStorage || r.json(t)["code"] != "instance_full" {
		t.Fatalf("budget: %d %s", r.status, r.body)
	}
	a.hack.SetInstanceUsage(nil)

	first := uniqueSlug()
	r = a.createEvent(t, org, first, nil)
	if r.status != http.StatusCreated {
		t.Fatalf("first: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events", a.createBody(uniqueSlug(), nil), a.key(org))
	if r.status != http.StatusTooManyRequests || r.json(t)["code"] != "too_many_events_today" {
		t.Fatalf("per-day: %d %s", r.status, r.body)
	}

	org2 := a.newPerson(t, "act")
	l2 := *config.Active()
	l2.EventCreatePerDay = 10
	l2.EventMaxActivePerOrganiser = 1
	config.SetActive(l2)
	s1 := uniqueSlug()
	if r := a.createEvent(t, org2, s1, nil); r.status != http.StatusCreated {
		t.Fatalf("active first: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events", a.createBody(uniqueSlug(), nil), a.key(org2))
	if r.status != http.StatusConflict || r.json(t)["code"] != "too_many_active_events" {
		t.Fatalf("active: %d %s", r.status, r.body)
	}
}

func TestHackNamesCheck(t *testing.T) {
	a := newHackApp(t)
	p := a.newPerson(t, "nam")
	free := uniqueSlug()
	r := a.at(t, "GET", "/v1/hack/names/"+free, nil, a.key(p))
	if r.status != 200 || r.json(t)["available"] != true {
		t.Fatalf("free: %d %s", r.status, r.body)
	}
	r = a.at(t, "GET", "/v1/hack/names/ab", nil, a.key(p))
	if r.status != 200 || r.json(t)["available"] != false || r.json(t)["code"] != "invalid_name" {
		t.Fatalf("short: %d %s", r.status, r.body)
	}
	r = a.at(t, "GET", "/v1/hack/names/join", nil, a.key(p))
	if r.status != 200 || r.json(t)["code"] != "name_reserved" {
		t.Fatalf("reserved: %d %s", r.status, r.body)
	}
}

func TestHackRoleIsolation(t *testing.T) {
	a := newHackApp(t)
	orgA, orgB := a.newPerson(t, "oa"), a.newPerson(t, "ob")
	part := a.newPerson(t, "pa")
	stranger := a.newPerson(t, "st")
	slugA, slugB := uniqueSlug(), uniqueSlug()
	if r := a.createEvent(t, orgA, slugA, nil); r.status != 201 {
		t.Fatalf("A: %d %s", r.status, r.body)
	}
	if r := a.createEvent(t, orgB, slugB, nil); r.status != 201 {
		t.Fatalf("B: %d %s", r.status, r.body)
	}
	a.openEvent(t, orgA, slugA)
	join := a.at(t, "GET", "/v1/hack/events/"+slugA, nil, a.key(orgA)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	jr := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(part))
	if jr.status != 200 {
		t.Fatalf("join: %d %s", jr.status, jr.body)
	}

	eventRoutes := []struct{ method, path string }{
		{"GET", "/v1/hack/events/" + slugA},
		{"PATCH", "/v1/hack/events/" + slugA},
		{"POST", "/v1/hack/events/" + slugA + "/stage"},
		{"POST", "/v1/hack/events/" + slugA + "/codes/join"},
		{"DELETE", "/v1/hack/events/" + slugA},
		{"GET", "/v1/hack/events/" + slugA + "/people"},
		{"DELETE", "/v1/hack/events/" + slugA + "/people/" + a.userID(t, part)},
		{"GET", "/v1/hack/events/" + slugA + "/teams"},
		{"POST", "/v1/hack/events/" + slugA + "/teams"},
		{"POST", "/v1/hack/events/" + slugA + "/teams/join"},
		{"POST", "/v1/hack/events/" + slugA + "/teams/leave"},
		{"POST", "/v1/hack/events/" + slugA + "/teams/nope/members"},
		{"DELETE", "/v1/hack/events/" + slugA + "/teams/nope/members/" + a.userID(t, part)},
		{"DELETE", "/v1/hack/events/" + slugA + "/teams/nope"},
	}
	bodies := map[string]any{
		"PATCH /v1/hack/events/" + slugA:                        map[string]string{"tagline": "x"},
		"POST /v1/hack/events/" + slugA + "/stage":              map[string]string{"stage": "building"},
		"POST /v1/hack/events/" + slugA + "/teams":              map[string]string{"name": "T"},
		"POST /v1/hack/events/" + slugA + "/teams/join":         map[string]string{"code": "abcdef"},
		"POST /v1/hack/events/" + slugA + "/teams/nope/members": map[string]string{"user_id": a.userID(t, part)},
	}
	for _, rt := range eventRoutes {
		body := bodies[rt.method+" "+rt.path]
		r := a.at(t, rt.method, rt.path, body, a.key(stranger))
		if r.status != 404 || r.json(t)["code"] != "event_not_found" {
			t.Errorf("stranger %s %s: %d %s", rt.method, rt.path, r.status, r.body)
		}
	}
	orgRoutes := []struct{ method, path string }{
		{"PATCH", "/v1/hack/events/" + slugA},
		{"POST", "/v1/hack/events/" + slugA + "/stage"},
		{"POST", "/v1/hack/events/" + slugA + "/codes/join"},
		{"DELETE", "/v1/hack/events/" + slugA},
		{"GET", "/v1/hack/events/" + slugA + "/people"},
		{"GET", "/v1/hack/events/" + slugA + "/teams"},
	}
	for _, rt := range orgRoutes {
		body := bodies[rt.method+" "+rt.path]
		r := a.at(t, rt.method, rt.path, body, a.key(part))
		if r.status != 404 || r.json(t)["code"] != "event_not_found" {
			t.Errorf("participant %s %s: %d %s", rt.method, rt.path, r.status, r.body)
		}
	}
	r := a.at(t, "GET", "/v1/hack/events/"+slugB, nil, a.key(orgA))
	if r.status != 404 || r.json(t)["code"] != "event_not_found" {
		t.Fatalf("cross-event: %d %s", r.status, r.body)
	}
	r = a.at(t, "GET", "/v1/hack/events/"+slugA+"/people", nil, a.key(orgB))
	if r.status != 404 {
		t.Fatalf("orgB people A: %d %s", r.status, r.body)
	}
}

func TestHackJoinJudgeAndCodes(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "jo")
	p1 := a.newPerson(t, "j1")
	p2 := a.newPerson(t, "j2")
	slug := uniqueSlug()
	cr := a.createEvent(t, org, slug, nil)
	if cr.status != 201 {
		t.Fatalf("create: %d %s", cr.status, cr.body)
	}
	orgView := cr.json(t)["organiser"].(map[string]any)
	join, judge := orgView["join_code"].(string), orgView["judge_code"].(string)

	r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "A"}, a.key(p1))
	if r.status != 409 || r.json(t)["code"] != "joining_closed" {
		t.Fatalf("draft join: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"display_name": "A"}, a.key(p1))
	if r.status != 400 || r.json(t)["code"] != "coc_required" {
		t.Fatalf("coc: %d %s", r.status, r.body)
	}
	r = a.at(t, "GET", "/v1/hack/join/not-a-code", nil, nil)
	if r.status != 404 || r.json(t)["code"] != "invalid_code" {
		t.Fatalf("bad code: %d %s", r.status, r.body)
	}

	a.openEvent(t, org, slug)
	info := a.at(t, "GET", "/v1/hack/join/"+strings.ToUpper(join[:4])+"-"+join[4:], nil, nil)
	if info.status != 200 || info.json(t)["joinable"] != true || info.json(t)["role"] != "participant" {
		t.Fatalf("join get: %d %s", info.status, info.body)
	}
	r = a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(p1))
	if r.status != 200 || r.json(t)["role"] != "participant" {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(p1))
	if r.status != 200 {
		t.Fatalf("rejoin: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/judge/"+judge, map[string]any{"accept_coc": true, "display_name": "Pat"}, a.key(p1))
	if r.status != 409 || r.json(t)["code"] != "already_member" {
		t.Fatalf("already: %d %s", r.status, r.body)
	}

	r = a.at(t, "POST", "/v1/hack/judge/"+judge, map[string]any{"accept_coc": true, "display_name": "Jay"}, a.key(p2))
	if r.status != 200 || r.json(t)["role"] != "judge" {
		t.Fatalf("judge: %d %s", r.status, r.body)
	}

	oldJoin := join
	regen := a.at(t, "POST", "/v1/hack/events/"+slug+"/codes/join", nil, a.key(org))
	if regen.status != 200 || regen.json(t)["code"] == oldJoin {
		t.Fatalf("regen: %d %s", regen.status, regen.body)
	}
	r = a.at(t, "GET", "/v1/hack/join/"+oldJoin, nil, nil)
	if r.status != 404 || r.json(t)["code"] != "invalid_code" {
		t.Fatalf("old code: %d %s", r.status, r.body)
	}

	// Results is not offered yet (M3); the stage rules still hold for it.
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "results"}, a.key(org)); r.status != 409 || r.json(t)["code"] != "stage_not_available" {
		t.Fatalf("results offered: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`UPDATE events SET stage = 'results' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	r = a.at(t, "POST", "/v1/hack/judge/"+judge, map[string]any{"accept_coc": true, "display_name": "New"}, a.key(a.newPerson(t, "j3")))
	if r.status != 409 || r.json(t)["code"] != "judging_closed" {
		t.Fatalf("judging closed: %d %s", r.status, r.body)
	}

	// RATE_LIMIT_EVENT_CODES_IP: 120 at once, then one a second.
	limited := 0
	for i := 0; i < 150; i++ {
		rr := a.at(t, "GET", "/v1/hack/join/zzzzzzzz", nil, nil)
		if rr.status == http.StatusTooManyRequests && rr.json(t)["code"] == "rate_limited" {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("expected rate_limited")
	}
}

func TestHackTeams(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "to")
	people := []*person{}
	for i := 0; i < 5; i++ {
		p := a.newPerson(t, "tm")
		people = append(people, &p)
	}
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	join := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	for _, p := range people {
		r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "M"}, a.key(*p))
		if r.status != 200 {
			t.Fatalf("join %s: %d %s", p.email, r.status, r.body)
		}
	}
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"team_size_max": 2}, a.key(org)); r.status != 200 {
		t.Fatalf("size: %d %s", r.status, r.body)
	}

	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Red Pandas"}, a.key(*people[0]))
	if r.status != 201 {
		t.Fatalf("create team: %d %s", r.status, r.body)
	}
	team := r.json(t)
	code := team["code"].(string)
	teamSlug := team["slug"].(string)
	if teamSlug == "" || code == "" {
		t.Fatalf("team: %v", team)
	}

	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Other"}, a.key(*people[0]))
	if r.status != 409 || r.json(t)["code"] != "already_in_team" {
		t.Fatalf("second team: %d %s", r.status, r.body)
	}

	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.key(*people[1]))
	if r.status != 200 {
		t.Fatalf("join team: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.key(*people[1]))
	if r.status != 200 {
		t.Fatalf("idempotent join: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.key(*people[2]))
	if r.status != 409 || r.json(t)["code"] != "team_full" {
		t.Fatalf("full: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": "zzzzzz"}, a.key(*people[2]))
	if r.status != 404 || r.json(t)["code"] != "team_not_found" {
		t.Fatalf("missing: %d %s", r.status, r.body)
	}

	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Blue"}, a.key(*people[2]))
	if r.status != 201 {
		t.Fatalf("blue: %d %s", r.status, r.body)
	}
	blue := r.json(t)["slug"].(string)

	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.key(*people[2]))
	if r.status != 409 || r.json(t)["code"] != "already_in_team" {
		t.Fatalf("one team: %d %s", r.status, r.body)
	}

	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/"+teamSlug+"/members", map[string]string{"user_id": a.userID(t, *people[3])}, a.key(org))
	if r.status != 409 || r.json(t)["code"] != "team_full" {
		t.Fatalf("org full: %d %s", r.status, r.body)
	}

	r = a.at(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+teamSlug+"/members/"+a.userID(t, *people[1]), nil, a.key(org))
	if r.status != http.StatusNoContent {
		t.Fatalf("remove member: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/"+teamSlug+"/members", map[string]string{"user_id": a.userID(t, *people[3])}, a.key(org))
	if r.status != 200 {
		t.Fatalf("move: %d %s", r.status, r.body)
	}

	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, a.key(*people[0]))
	if r.status != 200 {
		t.Fatalf("leave: %d %s", r.status, r.body)
	}

	r = a.at(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+blue, nil, a.key(org))
	if r.status != http.StatusNoContent {
		t.Fatalf("delete team: %d %s", r.status, r.body)
	}
	var n int
	if err := a.database.QueryRow(`SELECT COUNT(*) FROM event_teams WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, slug, blue).Scan(&n); err != nil || n != 0 {
		t.Fatalf("blue still there: %d %v", n, err)
	}

	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Solo"}, a.key(*people[4]))
	if r.status != 201 {
		t.Fatalf("solo: %d %s", r.status, r.body)
	}
	solo := r.json(t)["slug"].(string)
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, a.key(*people[4]))
	if r.status != 200 {
		t.Fatalf("leave solo: %d %s", r.status, r.body)
	}
	if err := a.database.QueryRow(`SELECT COUNT(*) FROM event_teams WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, slug, solo).Scan(&n); err != nil || n != 0 {
		t.Fatalf("empty team not deleted: %d %v", n, err)
	}

	// Submissions closed is not offered yet (M2); its team lock still holds.
	if _, err := a.database.Exec(`UPDATE events SET stage = 'closed' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Late"}, a.key(*people[0]))
	if r.status != 409 || r.json(t)["code"] != "teams_locked" {
		t.Fatalf("locked: %d %s", r.status, r.body)
	}

	r = a.at(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.userID(t, org), nil, a.key(org))
	if r.status != 409 || r.json(t)["code"] != "cannot_remove_organiser" {
		t.Fatalf("remove org: %d %s", r.status, r.body)
	}
	r = a.at(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.userID(t, *people[0]), nil, a.key(org))
	if r.status != http.StatusNoContent {
		t.Fatalf("remove person: %d %s", r.status, r.body)
	}
}

func TestHackConcurrentLastSlot(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "co")
	p1, p2, p3 := a.newPerson(t, "c1"), a.newPerson(t, "c2"), a.newPerson(t, "c3")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"team_size_max": 2}, a.key(org)); r.status != 200 {
		t.Fatalf("size: %d %s", r.status, r.body)
	}
	join := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	for _, p := range []person{p1, p2, p3} {
		if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "C"}, a.key(p)); r.status != 200 {
			t.Fatalf("join: %d %s", r.status, r.body)
		}
	}
	tr := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Race"}, a.key(p1))
	if tr.status != 201 {
		t.Fatalf("team: %d %s", tr.status, tr.body)
	}
	code := tr.json(t)["code"].(string)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var won, lost int
	run := func(p person) {
		defer wg.Done()
		r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.key(p))
		mu.Lock()
		defer mu.Unlock()
		if r.status == 200 {
			won++
		} else if r.status == 409 && r.json(t)["code"] == "team_full" {
			lost++
		} else {
			t.Errorf("join: %d %s", r.status, r.body)
		}
	}
	wg.Add(2)
	go run(p2)
	go run(p3)
	wg.Wait()
	if won != 1 || lost != 1 {
		t.Fatalf("last slot: won=%d lost=%d", won, lost)
	}
}

func TestHackAdmin(t *testing.T) {
	a := newHackApp(t)
	old := *config.Active()
	l := old
	l.EventCreatePerDay = 20
	l.EventMaxActivePerOrganiser = 20
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })
	org := a.newPerson(t, "ad")
	part := a.newPerson(t, "ap")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	join := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": "P"}, a.key(part)); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}

	if r := a.at(t, "GET", "/v1/admin/hack/events", nil, a.key(org)); r.status != 404 {
		t.Fatalf("non-admin list: %d %s", r.status, r.body)
	}
	list := a.at(t, "GET", "/v1/admin/hack/events", nil, a.adminH())
	if list.status != 200 {
		t.Fatalf("admin list: %d %s", list.status, list.body)
	}
	found := false
	var listed struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(list.body, &listed); err != nil {
		t.Fatalf("admin list shape: %v %s", err, list.body)
	}
	for _, ev := range listed.Events {
		if ev["slug"] == slug {
			if ev["participants"] != float64(1) {
				t.Fatalf("flat counts: %v", ev)
			}
			found = true
			if ev["organiser_name"] != "Ada Lovelace" || ev["contact_email"] != "ada@example.com" {
				t.Fatalf("organiser details: %v", ev)
			}
			if ev["created_by_email"] != org.email {
				t.Fatalf("creator: %v", ev["created_by_email"])
			}
			counts := ev["counts"].(map[string]any)
			if counts["participants"] != float64(1) {
				t.Fatalf("counts: %v", counts)
			}
		}
	}
	if !found {
		t.Fatal("event missing from admin list")
	}

	r := a.at(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "spam"}, a.adminH())
	if r.status != 200 || r.json(t)["taken_down"] != true {
		t.Fatalf("takedown: %d %s", r.status, r.body)
	}
	var suspended bool
	if err := a.database.QueryRow(`
		SELECT u.suspended_at IS NOT NULL FROM events e JOIN users u ON u.id = e.account_id WHERE e.slug = $1`, slug).Scan(&suspended); err != nil || !suspended {
		t.Fatalf("holding not suspended: %v %v", suspended, err)
	}
	r = a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"tagline": "nope"}, a.key(org))
	if r.status != 403 || r.json(t)["code"] != "event_taken_down" {
		t.Fatalf("write: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "X"}, a.key(part))
	if r.status != 403 || r.json(t)["code"] != "event_taken_down" {
		t.Fatalf("part write: %d %s", r.status, r.body)
	}
	r = a.at(t, "GET", "/v1/hack/join/"+join, nil, nil)
	if r.status != 410 || r.json(t)["code"] != "event_taken_down" {
		t.Fatalf("join get: %d %s", r.status, r.body)
	}

	r = a.at(t, "POST", "/v1/admin/hack/events/"+slug+"/restore", nil, a.adminH())
	if r.status != 200 || r.json(t)["taken_down"] != false {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if err := a.database.QueryRow(`
		SELECT u.suspended_at IS NOT NULL FROM events e JOIN users u ON u.id = e.account_id WHERE e.slug = $1`, slug).Scan(&suspended); err != nil || suspended {
		t.Fatalf("holding still suspended: %v %v", suspended, err)
	}
	r = a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"tagline": "back"}, a.key(org))
	if r.status != 200 {
		t.Fatalf("write after restore: %d %s", r.status, r.body)
	}

	r = a.at(t, "DELETE", "/v1/admin/hack/events/"+slug, nil, a.adminH())
	if r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	r = a.createEvent(t, org, slug, nil)
	if r.status != 201 {
		t.Fatalf("slug not freed: %d %s", r.status, r.body)
	}

	draft := uniqueSlug()
	if r := a.createEvent(t, org, draft, nil); r.status != 201 {
		t.Fatalf("draft: %d %s", r.status, r.body)
	}
	r = a.at(t, "DELETE", "/v1/hack/events/"+draft, nil, a.key(org))
	if r.status != http.StatusNoContent {
		t.Fatalf("org delete draft: %d %s", r.status, r.body)
	}
	r = a.createEvent(t, org, draft, nil)
	if r.status != 201 {
		t.Fatalf("draft slug not freed: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, draft)
	// Open but nobody joined: still deletable, and cannot be ended.
	if r := a.at(t, "POST", "/v1/hack/events/"+draft+"/stage", map[string]string{"stage": "archived"}, a.key(org)); r.status != 409 || r.json(t)["code"] != "archive_needs_participants" {
		t.Fatalf("end empty: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", "/v1/hack/events/"+draft, nil, a.key(org)); r.status != http.StatusNoContent {
		t.Fatalf("delete open empty: %d %s", r.status, r.body)
	}
	if r := a.createEvent(t, org, draft, nil); r.status != 201 {
		t.Fatalf("recreate: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, draft)
	jc := a.at(t, "GET", "/v1/hack/events/"+draft, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P"}, a.key(a.newPerson(t, "dp"))); r.status != 200 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
	r = a.at(t, "DELETE", "/v1/hack/events/"+draft, nil, a.key(org))
	if r.status != 409 || r.json(t)["code"] != "delete_only_empty" {
		t.Fatalf("delete with people: %d %s", r.status, r.body)
	}

	a.at(t, "POST", "/v1/hack/events/"+draft+"/stage", map[string]string{"stage": "archived"}, a.key(org))
	r = a.at(t, "POST", "/v1/hack/events/"+draft+"/stage", map[string]string{"stage": "open"}, a.key(org))
	if r.status != 409 || r.json(t)["code"] != "event_closed" {
		t.Fatalf("leave archived: %d %s", r.status, r.body)
	}
}

func TestHackListAndGet(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "ls")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	list := a.at(t, "GET", "/v1/hack/events", nil, a.key(org))
	if list.status != 200 {
		t.Fatalf("list: %d %s", list.status, list.body)
	}
	found := false
	for _, ev := range jsonArr(t, list) {
		if ev["slug"] == slug && ev["role"] == "organiser" && ev["manage_url"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("list: %s", list.body)
	}
}

func TestHackDefaultCoC(t *testing.T) {
	if !strings.Contains(HackDefaultCoC, "respect") || !strings.Contains(strings.ToLower(HackDefaultCoC), "harass") {
		t.Fatalf("CoC: %s", HackDefaultCoC)
	}
}

func TestHackNameTakenHelper(t *testing.T) {
	a := newHackApp(t)
	ctx := context.Background()
	taken, err := a.hack.NameTaken(ctx, "join")
	if err != nil || !taken {
		t.Fatalf("reserved: %v %v", taken, err)
	}
	p := a.newPerson(t, "nt")
	slug := uniqueSlug()
	if r := a.createEvent(t, p, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	taken, err = a.hack.NameTaken(ctx, slug)
	if err != nil || !taken {
		t.Fatalf("event slug: %v %v", taken, err)
	}
	free, err := a.hack.NameTaken(ctx, uniqueSlug())
	if err != nil || free {
		t.Fatalf("free: %v %v", free, err)
	}
}

func TestHackEventURL(t *testing.T) {
	h := NewHackHandler(nil, "https://simple-hack.app", "simple-hack.app")
	if got := h.EventURL("spring"); got != "https://spring.simple-hack.app/" {
		t.Fatalf("EventURL: %s", got)
	}
}

func TestHackReviewFixes(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "rforg")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { a.cleanupEvent(slug) })
	a.openEvent(t, org, slug)
	jc := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	join := func(label string) person {
		p := a.newPerson(t, label)
		if r := a.at(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": label}, a.key(p)); r.status != 200 {
			t.Fatalf("join %s: %d %s", label, r.status, r.body)
		}
		return p
	}
	p1, p2, p3 := join("rfa"), join("rfb"), join("rfc")

	// A participant's team answer carries no emails or account ids.
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Owls"}, a.key(p1))
	if r.status != 201 || strings.Contains(string(r.body), "@") || strings.Contains(string(r.body), "user_id") {
		t.Fatalf("create team leaks: %d %s", r.status, r.body)
	}
	code := r.json(t)["code"].(string)
	r = a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.key(p2))
	if r.status != 200 || strings.Contains(string(r.body), "@") || strings.Contains(string(r.body), "user_id") {
		t.Fatalf("join team leaks: %d %s", r.status, r.body)
	}
	// Team names are unique, whatever the case.
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "OWLS"}, a.key(p3)); r.status != 409 || r.json(t)["code"] != "team_name_taken" {
		t.Fatalf("duplicate name: %d %s", r.status, r.body)
	}
	// Taking someone off a team gives the team a new code.
	if r := a.at(t, "DELETE", "/v1/hack/events/"+slug+"/teams/owls/members/"+a.userID(t, p2), nil, a.key(org)); r.status != http.StatusNoContent {
		t.Fatalf("take off: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, a.key(p2)); r.status != 404 || r.json(t)["code"] != "team_not_found" {
		t.Fatalf("old team code still works: %d %s", r.status, r.body)
	}
	// Single-line fields refuse line breaks and invisible formatting.
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"title": "A\nB"}, a.key(org)); r.status != 400 || r.json(t)["code"] != "invalid_title" {
		t.Fatalf("newline title: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"title": "Bidi \u202egnp.exe"}, a.key(org)); r.status != 400 {
		t.Fatalf("bidi title: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"title": "Family 👨\u200d👩\u200d👧"}, a.key(org)); r.status != 200 {
		t.Fatalf("emoji ZWJ title: %d %s", r.status, r.body)
	}
	// Clearing the end date.
	if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"ends_at": ""}, a.key(org)); r.status != 200 || r.json(t)["event"].(map[string]any)["ends_at"] != nil {
		t.Fatalf("clear ends_at: %d %s", r.status, r.body)
	}
	// Only the offered stages can be set.
	for _, st := range []string{"closed", "judging", "results"} {
		if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": st}, a.key(org)); r.status != 409 || r.json(t)["code"] != "stage_not_available" {
			t.Fatalf("stage %s: %d %s", st, r.status, r.body)
		}
	}
	// The admin reads read-only and cannot join.
	if r := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.adminH()); r.status != 200 || r.json(t)["admin_view"] != true {
		t.Fatalf("admin view: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "Admin"}, a.adminH()); r.status != 409 || r.json(t)["code"] != "admin_cannot_join" {
		t.Fatalf("admin join: %d %s", r.status, r.body)
	}
	// The list carries the event's time zone.
	found := false
	for _, ev := range jsonArr(t, a.at(t, "GET", "/v1/hack/events", nil, a.key(org))) {
		if ev["slug"] == slug {
			found = ev["time_zone"] != nil
		}
	}
	if !found {
		t.Fatal("list: no time_zone")
	}
	// New reserved names.
	if r := a.at(t, "GET", "/v1/hack/names/mta-sts", nil, a.key(org)); r.status != 200 || r.json(t)["available"] != false {
		t.Fatalf("mta-sts: %d %s", r.status, r.body)
	}
}

func TestHackReviewRound2(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "r2org")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { a.cleanupEvent(slug) })
	a.openEvent(t, org, slug)
	jc := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)

	// A venue's shared address spent by anonymous lookups does not stop a
	// signed-in person from joining (they are metered by account).
	for i := 0; i < 200; i++ {
		a.at(t, "GET", "/v1/hack/join/zzzzzzzz", nil, nil)
	}
	if r := a.at(t, "GET", "/v1/hack/join/zzzzzzzz", nil, nil); r.status != http.StatusTooManyRequests {
		t.Fatalf("anonymous lookups not limited: %d", r.status)
	}
	p1 := a.newPerson(t, "r2p1")
	if r := a.at(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P1"}, a.key(p1)); r.status != 200 {
		t.Fatalf("signed-in join after the address ran dry: %d %s", r.status, r.body)
	}

	// Real text is accepted; blank-looking names are not; inner spaces collapse.
	for _, title := range []string{"Glasgow Hack 🏴\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F", "مرحبا ‏ABC", "Hackathon­Name"} {
		if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"title": title}, a.key(org)); r.status != 200 {
			t.Fatalf("title %q: %d %s", title, r.status, r.body)
		}
	}
	for _, title := range []string{"ㅤ", "⠀⠀", "a b"} {
		if r := a.at(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"title": title}, a.key(org)); r.status != 400 {
			t.Fatalf("title %q accepted: %d %s", title, r.status, r.body)
		}
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Night  Owls"}, a.key(p1)); r.status != 201 || r.json(t)["name"] != "Night Owls" {
		t.Fatalf("collapse spaces: %d %s", r.status, r.body)
	}
	p2 := a.newPerson(t, "r2p2")
	if r := a.at(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P2"}, a.key(p2)); r.status != 200 {
		t.Fatalf("join p2: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "night owls"}, a.key(p2)); r.status != 409 || r.json(t)["code"] != "team_name_taken" {
		t.Fatalf("duplicate after collapse: %d %s", r.status, r.body)
	}

	// Two teams of one name at the same moment: one is created, the other is
	// told the name is taken (never a server error).
	p3, p4 := a.newPerson(t, "r2p3"), a.newPerson(t, "r2p4")
	for _, p := range []person{p3, p4} {
		if r := a.at(t, "POST", "/v1/hack/join/"+jc, map[string]any{"accept_coc": true, "display_name": "P"}, a.key(p)); r.status != 200 {
			t.Fatalf("join: %d %s", r.status, r.body)
		}
	}
	codes := make(chan int, 2)
	for _, p := range []person{p3, p4} {
		go func(p person) {
			codes <- a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": "Race Team"}, a.key(p)).status
		}(p)
	}
	got := []int{<-codes, <-codes}
	if !((got[0] == 201 && got[1] == 409) || (got[0] == 409 && got[1] == 201)) {
		t.Fatalf("race: %v", got)
	}

	// An organiser of a running event cannot delete the account; after the
	// event ends they can, and their name and address leave the event.
	hackMode = true
	t.Cleanup(func() { hackMode = false })
	orgID := a.userID(t, org)
	if code, _, err := hackAccountDeleteBlock(context.Background(), a.database, orgID); err != nil || code != "organises_events" {
		t.Fatalf("running event: %q %v", code, err)
	}
	var holding string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&holding); err != nil {
		t.Fatal(err)
	}
	if code, _, err := hackAccountDeleteBlock(context.Background(), a.database, holding); err != nil || code != "event_account" {
		t.Fatalf("holding account: %q %v", code, err)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, a.key(org)); r.status != 200 {
		t.Fatalf("end: %d %s", r.status, r.body)
	}
	// Nothing changes after the end.
	if r := a.at(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.userID(t, p2), nil, a.key(org)); r.status != 409 || r.json(t)["code"] != "event_closed" {
		t.Fatalf("remove after end: %d %s", r.status, r.body)
	}
	if code, _, err := hackAccountDeleteBlock(context.Background(), a.database, orgID); err != nil || code != "" {
		t.Fatalf("ended event: %q %v", code, err)
	}
	var name, contact string
	if err := a.database.QueryRow(`SELECT organiser_name, contact_email FROM events WHERE slug = $1`, slug).Scan(&name, &contact); err != nil || name != "" || contact != "" {
		t.Fatalf("organiser details kept: %q %q %v", name, contact, err)
	}
}
