package hacktest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/model"
)

// The state a feature's suite starts from (events, stages, members, teams,
// entry rules, co-organisers), made through the core's functions so a suite
// never mounts another feature's routes. Where a call answers, the answer is
// what the feature's route writes for it.

// caller is the user behind an X-API-Key header of a person or the admin.
func (a *App) caller(t *testing.T, h map[string]string) (string, bool) {
	t.Helper()
	key := h["X-API-Key"]
	if key == a.Admin {
		return a.adminID, true
	}
	id, ok := a.ids[key]
	if !ok {
		t.Fatalf("hacktest: no person for that key")
	}
	return id, false
}

// answer is what a route writes: status and body, or the refusal for err.
func answer(status int, body any, err error) Resp {
	rec := httptest.NewRecorder()
	if err != nil {
		core.WriteError(rec, err)
	} else {
		httpx.WriteJSON(rec, status, body)
	}
	return Resp{rec.Code, rec.Header(), rec.Body.Bytes()}
}

// CreateEvent creates the event p asks for (CreateBody with extra) and answers
// as POST /v1/hack/events does; the event is removed when the test ends.
func (a *App) CreateEvent(t *testing.T, p Person, slug string, extra map[string]any) Resp {
	t.Helper()
	ctx := context.Background()
	var req struct {
		Slug                 string `json:"slug"`
		Title                string `json:"title"`
		OrganiserName        string `json:"organiser_name"`
		Organisation         string `json:"organisation"`
		ContactEmail         string `json:"contact_email"`
		Purpose              string `json:"purpose"`
		ExpectedParticipants *int   `json:"expected_participants"`
		StartsAt             string `json:"starts_at"`
		EndsAt               string `json:"ends_at"`
		TimeZone             string `json:"time_zone"`
	}
	raw, _ := json.Marshal(a.CreateBody(slug, extra))
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	in, err := events.CheckNewEvent(events.NewEventInput(req))
	if err != nil {
		return answer(0, nil, err)
	}
	uid, _ := a.caller(t, a.Key(p))
	l := a.Hack.Limits
	ev, member, err := a.Hack.Events.CreateEvent(ctx, uid, strings.ToLower(slug), in.Title, in.OrganiserName, in.Organisation, in.ContactEmail, in.Purpose, in.TimeZone, in.ExpectedParticipants, in.StartsAt, in.EndsAt, l.EventCreatePerDay, l.EventMaxActivePerOrganiser, l.EventTeamSizeDefault, events.JoinCodeLen, events.JudgeCodeLen)
	if err != nil {
		return answer(0, nil, err)
	}
	t.Cleanup(func() { a.CleanupEvent(slug) })
	return answer(http.StatusCreated, a.Hack.EventView(ctx, ev, member, true), nil)
}

// SetStage moves the event to stage for the organiser (or admin) behind h,
// with the assignments, deadline pin and certificate the stage route adds,
// and answers as POST .../stage does.
func (a *App) SetStage(t *testing.T, slug, stage string, h map[string]string) Resp {
	t.Helper()
	ctx := context.Background()
	uid, admin := a.caller(t, h)
	c, err := a.Hack.Members.Caller(ctx, eventSlug(slug), uid, admin, true, "organiser")
	if err != nil {
		return answer(0, nil, err)
	}
	updated, current, err := a.Hack.Events.SetStage(ctx, c.Event, stage)
	if err != nil {
		return answer(0, nil, err)
	}
	if err := a.Hack.Entries.AssignOnStartJudging(ctx, updated, current); err != nil {
		t.Fatalf("stage assignments: %v", err)
	}
	a.Hack.Links.PinDue(ctx, updated.ID)
	a.Hack.Links.RequestEventCert(updated)
	return answer(http.StatusOK, a.Hack.EventView(ctx, updated, c.Member, true), nil)
}

// OpenEvent opens sign-up.
func (a *App) OpenEvent(t *testing.T, p Person, slug string) {
	t.Helper()
	if r := a.SetStage(t, slug, "open", a.Key(p)); r.Status != 200 {
		t.Fatalf("open: %d %s", r.Status, r.Body)
	}
}

func (a *App) event(t *testing.T, slug string) model.Event {
	t.Helper()
	ev, err := a.Hack.Events.EventBySlug(context.Background(), slug)
	if err != nil {
		t.Fatalf("event %s: %v", slug, err)
	}
	return ev
}

// JoinCodes are the event's join and judge codes.
func (a *App) JoinCodes(t *testing.T, org Person, slug string) (join, judge string) {
	t.Helper()
	ev := a.event(t, slug)
	return ev.JoinCode, ev.JudgeCode
}

func (a *App) joinByCode(t *testing.T, code string, p Person, name string, judge bool) {
	t.Helper()
	uid, _ := a.caller(t, a.Key(p))
	if _, _, _, err := a.Hack.Events.JoinByCode(context.Background(), events.NormalizeCode(code), uid, name, judge, nil, events.ValidateSignupAnswers); err != nil {
		t.Fatalf("join %s: %v", name, err)
	}
}

// JoinParticipant joins p by the event's join code, accepting the code of conduct.
func (a *App) JoinParticipant(t *testing.T, join string, p Person, name string) {
	t.Helper()
	a.joinByCode(t, join, p, name, false)
}

// JoinJudge joins p as a judge by the event's judge code.
func (a *App) JoinJudge(t *testing.T, judgeCode string, p Person, name string) {
	t.Helper()
	a.joinByCode(t, judgeCode, p, name, true)
}

func (a *App) participant(t *testing.T, slug string, p Person) model.Event {
	t.Helper()
	uid, _ := a.caller(t, a.Key(p))
	c, err := a.Hack.Members.Caller(context.Background(), slug, uid, false, true, "participant")
	if err != nil {
		t.Fatalf("participant %s: %v", slug, err)
	}
	return c.Event
}

// CreateTeam has participant p create team name.
func (a *App) CreateTeam(t *testing.T, slug string, p Person, name string) model.EventTeam {
	t.Helper()
	uid, _ := a.caller(t, a.Key(p))
	team, err := a.Hack.Teams.CreateTeam(context.Background(), a.participant(t, slug, p), uid, name, "", a.Hack.Links.TeamSiteFlatHostsOn())
	if err != nil {
		t.Fatalf("create team %s: %v", name, err)
	}
	return team
}

// CreateTeamAndJoin has p create "name" and returns its team id and join code.
func (a *App) CreateTeamAndJoin(t *testing.T, slug string, p Person, name string) (teamID, code string) {
	t.Helper()
	team := a.CreateTeam(t, slug, p, name)
	return team.ID, team.Code
}

// JoinTeamByCode has participant p join the team with code.
func (a *App) JoinTeamByCode(t *testing.T, slug, code string, p Person) {
	t.Helper()
	uid, _ := a.caller(t, a.Key(p))
	if _, err := a.Hack.Teams.JoinTeam(context.Background(), a.participant(t, slug, p), uid, code); err != nil {
		t.Fatalf("join team: %v", err)
	}
}

// PutEntryRules sets the event's entry fields.
func (a *App) PutEntryRules(t *testing.T, slug string, fields map[string]string) {
	t.Helper()
	if _, err := a.Hack.Entries.PutRules(context.Background(), a.event(t, slug).ID, entries.RulesUpdate{Fields: fields}); err != nil {
		t.Fatalf("entry rules: %v", err)
	}
}

// AddOrganiser makes the caller identified by h a co-organiser through the
// organiser invite, so the platform team membership follows.
func (w *JudgingWorld) AddOrganiser(t *testing.T, h map[string]string) {
	t.Helper()
	a := w.A
	ctx := context.Background()
	org, _ := a.caller(t, a.Key(w.Org))
	code, _, err := a.Hack.Events.CreateOrganiserInvite(ctx, a.event(t, w.Slug).ID, org)
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	uid, _ := a.caller(t, h)
	if _, m, err := a.Hack.Events.AcceptOrganiserInvite(ctx, code, uid, "Co Organiser"); err != nil || m.Role != "organiser" {
		t.Fatalf("co-organiser join: %v %v", err, m.Role)
	}
}

// eventSlug accepts an event's slug or its path (/v1/hack/events/<slug>).
func eventSlug(s string) string { return strings.TrimPrefix(s, "/v1/hack/events/") }

// PutScores has the judge (or judging organiser) behind h score team with
// body ({"scores": [...], "comment": ...}), with the refusals PUT
// .../judge/scores/{team} gives; a success answers 200.
func (a *App) PutScores(t *testing.T, event, team string, body any, h map[string]string) Resp {
	t.Helper()
	ctx := context.Background()
	uid, admin := a.caller(t, h)
	c, err := a.Hack.Members.Caller(ctx, eventSlug(event), uid, admin, true, "judge", "organiser")
	if err != nil {
		return answer(0, nil, err)
	}
	if c.Event.JudgingLockedAt.Valid {
		return answer(0, nil, &events.Error{Code: "scores_locked", Message: "Judging is locked."})
	}
	if !a.Hack.ScoringStage(ctx, c.Event) {
		return answer(0, nil, &events.Error{Code: "scoring_not_open", Message: "Scoring opens when judging starts."})
	}
	if err := a.Hack.Entries.CheckScoreConflict(ctx, c.Event.ID, uid, team); err != nil {
		return answer(0, nil, err)
	}
	var req struct {
		Scores  []entries.ScoreInput `json:"scores"`
		Comment *string              `json:"comment"`
	}
	raw, _ := json.Marshal(body)
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = a.Hack.Entries.PutJudgeScores(ctx, c.Event.ID, uid, team, req.Scores, req.Comment, a.Hack.ScoringStage)
	return answer(http.StatusOK, map[string]any{}, err)
}
