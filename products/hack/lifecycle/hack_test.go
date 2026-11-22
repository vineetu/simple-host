package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	productconfig "github.com/vsriram/simple-host/products/hack/config"
	productedition "github.com/vsriram/simple-host/products/hack/edition"

	accountapp "github.com/vsriram/simple-host/host/access"
)

func TestHackCreateSuccessAndHoldingAccount(t *testing.T) {
	a := newHackApp(t)
	p := a.NewPerson(t, "org")
	slug := hacktest.UniqueSlug()
	r := a.CreateEvent(t, p, slug, nil)
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	j := r.JSON(t)
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
	err := a.DB.QueryRow(`
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
	p := a.NewPerson(t, "val")
	slug := hacktest.UniqueSlug()
	cases := []struct {
		name   string
		extra  map[string]any
		status int
		code   string
	}{
		{"short", map[string]any{"slug": "ab"}, 400, "invalid_name"},
		{"reserved-join", map[string]any{"slug": "join"}, 400, "name_reserved"},
		{"empty-title", map[string]any{"title": ""}, 400, "invalid_title"},
		{"bad-email", map[string]any{"contact_email": "not-an-email"}, 400, "invalid_contact_email"},
		{"zero-people", map[string]any{"expected_participants": 0}, 400, "invalid_expected_participants"},
		{"ends-before", map[string]any{"starts_at": "2026-10-05", "ends_at": "2026-10-01"}, 400, "invalid_ends_at"},
		{"bad-tz", map[string]any{"time_zone": "Not/AZone"}, 400, "invalid_time_zone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := a.CreateBody(slug, tc.extra)
			r := a.At(t, "POST", "/v1/hack/events", body, a.Key(p))
			if r.Status != tc.status || r.JSON(t)["code"] != tc.code {
				t.Fatalf("got %d %s want %d %s", r.Status, r.Body, tc.status, tc.code)
			}
		})
	}
}

// TestHackEventEndOfDayNotBeforeStart: SIM-379 fix. A date-only end (local
// midnight) means the whole of that day, like everywhere else it's read
// (events.EventOver) -- so a same-day timed start with a date-only end must
// be accepted on create, not rejected as "ends_at must not be before
// starts_at" just because the literal midnight instant precedes the start's
// own time of day. Clearing an End's time back to date-only (PATCH) must be
// accepted the same way. An end whose calendar date is genuinely before the
// start's is still refused, on create and update alike.
func TestHackEventEndOfDayNotBeforeStart(t *testing.T) {
	a := newHackApp(t)
	p := a.NewPerson(t, "eod")
	slug := hacktest.UniqueSlug()

	r := a.CreateEvent(t, p, slug, map[string]any{
		"starts_at": "2027-03-12T09:00",
		"ends_at":   "2027-03-12", // date-only, same calendar date as the timed start
		"time_zone": "Asia/Kolkata",
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create same-day timed start + date-only end: %d %s", r.Status, r.Body)
	}

	// Setting, then clearing, a same-day timed End back to date-only.
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"ends_at": "2027-03-12T18:00"}, a.Key(p)); r.Status != 200 {
		t.Fatalf("set same-day timed end: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"ends_at": "2027-03-12"}, a.Key(p)); r.Status != 200 {
		t.Fatalf("clear same-day timed end back to date-only: %d %s", r.Status, r.Body)
	}

	// An end date genuinely before the start date is still refused.
	if r := a.At(t, "PATCH", "/v1/hack/events/"+slug, map[string]any{"ends_at": "2027-03-11"}, a.Key(p)); r.Status != 400 || r.JSON(t)["code"] != "invalid_ends_at" {
		t.Fatalf("end before start should still be refused: %d %s", r.Status, r.Body)
	}
}

func TestHackCreateNameTakenHandlePeerBudgetLimits(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "lim")
	other := a.NewPerson(t, "hold")

	taken := hacktest.UniqueSlug()
	if _, err := a.DB.Exec(`UPDATE users SET handle = $1 WHERE username = $2`, taken, other.Email); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "POST", "/v1/hack/events", a.CreateBody(taken, nil), a.Key(org))
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "name_taken" {
		t.Fatalf("handle taken: %d %s", r.Status, r.Body)
	}

	a.lc.NamePeer = func(ctx context.Context, name string) (bool, error) { return true, nil }
	peerSlug := hacktest.UniqueSlug()
	r = a.At(t, "POST", "/v1/hack/events", a.CreateBody(peerSlug, nil), a.Key(org))
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "name_taken" {
		t.Fatalf("peer taken: %d %s", r.Status, r.Body)
	}

	a.lc.NamePeer = func(ctx context.Context, name string) (bool, error) { return false, errors.New("peer down") }
	r = a.At(t, "POST", "/v1/hack/events", a.CreateBody(hacktest.UniqueSlug(), nil), a.Key(org))
	if r.Status != http.StatusServiceUnavailable || r.JSON(t)["code"] != "name_check_unavailable" {
		t.Fatalf("peer err: %d %s", r.Status, r.Body)
	}
	check := a.At(t, "GET", "/v1/hack/names/"+hacktest.UniqueSlug(), nil, a.Key(org))
	if check.Status != http.StatusServiceUnavailable || check.JSON(t)["code"] != "name_check_unavailable" {
		t.Fatalf("names peer err: %d %s", check.Status, check.Body)
	}
	a.lc.NamePeer = nil

	a.lc.InstanceUsage = func(ctx context.Context) (int64, error) { return 9 * 1024 * 1024 * 1024, nil }
	l := productconfig.DefaultLimits()
	l.HackInstanceBudgetGB = 10
	l.EventCreatePerDay = 1
	l.EventMaxActivePerOrganiser = 1
	hacktest.SetLimits(a.App, l)
	r = a.At(t, "POST", "/v1/hack/events", a.CreateBody(hacktest.UniqueSlug(), nil), a.Key(org))
	if r.Status != http.StatusInsufficientStorage || r.JSON(t)["code"] != "instance_full" {
		t.Fatalf("budget: %d %s", r.Status, r.Body)
	}
	a.lc.InstanceUsage = nil

	first := hacktest.UniqueSlug()
	r = a.CreateEvent(t, org, first, nil)
	if r.Status != http.StatusCreated {
		t.Fatalf("first: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events", a.CreateBody(hacktest.UniqueSlug(), nil), a.Key(org))
	if r.Status != http.StatusTooManyRequests || r.JSON(t)["code"] != "too_many_events_today" {
		t.Fatalf("per-day: %d %s", r.Status, r.Body)
	}

	deleted := a.At(t, "DELETE", "/v1/hack/events/"+first, nil, a.Key(org))
	if deleted.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.Status, deleted.Body)
	}
	if replacement := a.CreateEvent(t, org, hacktest.UniqueSlug(), nil); replacement.Status != http.StatusCreated {
		t.Fatalf("deleted event counted: %d %s", replacement.Status, replacement.Body)
	}

	org2 := a.NewPerson(t, "act")
	l.EventCreatePerDay = 10
	l.EventMaxActivePerOrganiser = 1
	hacktest.SetLimits(a.App, l)
	s1 := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org2, s1, nil); r.Status != http.StatusCreated {
		t.Fatalf("active first: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", "/v1/hack/events", a.CreateBody(hacktest.UniqueSlug(), nil), a.Key(org2))
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "too_many_active_events" {
		t.Fatalf("active: %d %s", r.Status, r.Body)
	}
}

func TestHackNamesCheck(t *testing.T) {
	a := newHackApp(t)
	p := a.NewPerson(t, "nam")
	free := hacktest.UniqueSlug()
	r := a.At(t, "GET", "/v1/hack/names/"+free, nil, a.Key(p))
	if r.Status != 200 || r.JSON(t)["available"] != true {
		t.Fatalf("free: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", "/v1/hack/names/ab", nil, a.Key(p))
	if r.Status != 200 || r.JSON(t)["available"] != false || r.JSON(t)["code"] != "invalid_name" {
		t.Fatalf("short: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", "/v1/hack/names/join", nil, a.Key(p))
	if r.Status != 200 || r.JSON(t)["code"] != "name_reserved" {
		t.Fatalf("reserved: %d %s", r.Status, r.Body)
	}
}

func TestHackListAndGet(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "ls")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	list := a.At(t, "GET", "/v1/hack/events", nil, a.Key(org))
	if list.Status != 200 {
		t.Fatalf("list: %d %s", list.Status, list.Body)
	}
	found := false
	for _, ev := range hacktest.JSONArr(t, list) {
		if ev["slug"] == slug && ev["role"] == "organiser" && ev["manage_url"] != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("list: %s", list.Body)
	}
}

func TestHackReviewRound4(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "r4org")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != 201 {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { a.CleanupEvent(slug) })
	a.OpenEvent(t, org, slug)
	ctx := context.Background()
	var holding string
	if err := a.DB.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&holding); err != nil {
		t.Fatal(err)
	}
	if held, err := accountapp.HoldingAccount(ctx, a.DB, strings.ToUpper(holding)); err != nil || !held {
		t.Fatalf("upper-case holding id: %v %v", held, err)
	}
	// An organiser leaving with a taken-down event ends it, so a later
	// restore cannot bring back an event nobody manages.
	if _, err := a.DB.Exec(`UPDATE events SET taken_down_at = now() WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	if code, _, err := hacktest.ErasureBlockForTest(t, ctx, a.DB, a.UserID(t, org)); err != nil || code != "" {
		t.Fatalf("delete: %q %v", code, err)
	}
	var stage string
	if err := a.DB.QueryRow(`SELECT stage FROM events WHERE slug = $1`, slug).Scan(&stage); err != nil || stage != "archived" {
		t.Fatalf("stage after organiser left: %q %v", stage, err)
	}
}

func newHackAppWithLimits(t *testing.T, limits productconfig.Limits) *hackApp {
	a := newHackApp(t, productedition.ForKindWithLimits(productedition.Hack, limits.Limits))
	a.hack.SetProductLimits(limits)
	return a
}
