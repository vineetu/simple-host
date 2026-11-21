package members

import (
	"context"

	"github.com/vsriram/simple-host/host/sqlkit"

	model "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// LeavePreview is the GET .../leave answer: what leaving would do for the caller right now.
type LeavePreview struct {
	Event model.Event `json:"-"`
	Slug  string      `json:"slug"`
	Title string      `json:"title"`
	Stage string      `json:"stage"`
	Role  string      `json:"role"`
	// ApprovalStatus tells a rejected applicant from a pending one: the same "applicant" case, but different client copy.
	ApprovalStatus    string           `json:"approval_status"`
	Case              string           `json:"case"`
	Confirm           string           `json:"confirm"`
	Team              *LeaveTeam       `json:"team,omitempty"`
	ScoredTeams       *int             `json:"scored_teams,omitempty"`
	OrganisersStaying []string         `json:"organisers_staying,omitempty"`
	Successors        []LeaveSuccessor `json:"successors,omitempty"`
	CloseOptions      map[string]any   `json:"close_options,omitempty"`
	Rejoin            string           `json:"rejoin"`
	Allowed           bool             `json:"allowed"`
	Blocked           *LeaveBlock      `json:"blocked"`
}

// LeaveTeam is what leaving does to a participant's team.
type LeaveTeam struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	Others      []string `json:"others"`
	OthersCount int      `json:"others_count"`
	// DeletedOnLeave and Frozen come from TeamKeptIfEmptied, the predicate the write path uses, so the preview never promises a team "stays" when leaving would delete it.
	DeletedOnLeave bool `json:"deleted_on_leave"`
	Frozen         bool `json:"frozen"`
}

// LeaveSuccessor is an organiser the creator may hand the event to.
type LeaveSuccessor struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// LeaveBlock is why nobody may leave the event now.
type LeaveBlock struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// rejoinRoutes says how someone in each leave case could come back, and in which stages; a case missing here cannot rejoin.
var rejoinRoutes = map[string]struct {
	route  string
	stages func(string) bool
}{
	"participant":  {"join", func(st string) bool { return st == "open" || st == "building" }},
	"applicant":    {"join", func(st string) bool { return st == "open" || st == "building" }},
	"judge":        {"judge_link", func(st string) bool { return st != "results" && st != "archived" }},
	"co_organiser": {"organiser_invite", func(string) bool { return true }},
	"creator":      {"organiser_invite", func(string) bool { return true }},
}

// rejoinFor names how a leaver in leaveCase could rejoin at this stage.
func rejoinFor(leaveCase, stage string) string {
	if r, ok := rejoinRoutes[leaveCase]; ok && r.stages(stage) {
		return r.route
	}
	return "none"
}

// PreviewLeave describes, without writing or locking, what leaving the event at slug would do for the caller.
func (s *Service) PreviewLeave(ctx context.Context, slug, userID string) (p LeavePreview, err error) {
	if p.Event, err = eventrepo.GetEventBySlug(ctx, s.db, slug); err != nil {
		return p, err
	}
	ev := p.Event
	member, status, err := eventrepo.LeaveSelf(ctx, s.db, ev.ID, userID, false)
	if err != nil {
		return p, err
	}
	others, err := eventrepo.OtherEventOrganisers(ctx, s.db, ev.ID, userID)
	if err != nil {
		return p, err
	}
	p.Slug, p.Title, p.Stage, p.Confirm = ev.Slug, ev.Title, ev.Stage, ev.Slug
	p.Role, p.ApprovalStatus = member.Role, status
	p.Case = leaveCase(ev, member, status, userID, others)
	p.Rejoin = rejoinFor(p.Case, ev.Stage)
	if refusal := leaveRefusal(ev); refusal != nil {
		p.Blocked = &LeaveBlock{Code: refusal.Code, Message: refusal.Message}
	}
	p.Allowed = p.Blocked == nil
	return p, s.previewCase(ctx, &p, member, userID, others)
}

// previewCase adds the facts only the caller's leave case needs.
func (s *Service) previewCase(ctx context.Context, p *LeavePreview, member model.EventMember, userID string, others []model.OtherOrganiser) (err error) {
	switch p.Case {
	case "participant":
		if member.TeamID.Valid {
			p.Team, err = previewTeam(ctx, s.db, member.TeamID.String, userID)
		}
		return err
	case "applicant", "":
		return nil
	}
	if p.ScoredTeams, err = scoredTeams(ctx, s.db, p.Event.ID, userID); err != nil {
		return err
	}
	switch p.Case {
	case "co_organiser":
		p.OrganisersStaying = firstThree(organiserNames(others))
	case "creator":
		for _, o := range others {
			p.Successors = append(p.Successors, LeaveSuccessor{UserID: o.UserID, DisplayName: o.DisplayName, Email: o.Email})
		}
	case "last_organiser":
		p.CloseOptions, err = closeOptions(ctx, s.db, p.Event.ID)
	}
	return err
}

// previewTeam describes what leaving does to the caller's team.
func previewTeam(ctx context.Context, db sqlkit.Querier, teamID, userID string) (*LeaveTeam, error) {
	team, err := teamrepo.GetEventTeamByID(ctx, db, teamID)
	if err != nil {
		return nil, err
	}
	people, err := teamrepo.ListTeamPeople(ctx, db, team.ID)
	if err != nil {
		return nil, err
	}
	kept, err := eventrepo.TeamKeptIfEmptied(ctx, db, team.ID)
	if err != nil {
		return nil, err
	}
	teammates := make([]string, 0, len(people))
	for _, p := range people {
		if p.UserID != userID {
			teammates = append(teammates, p.DisplayName)
		}
	}
	return &LeaveTeam{Slug: team.Slug, Name: team.Name, Others: firstThree(teammates), OthersCount: len(teammates),
		DeletedOnLeave: len(teammates) == 0 && !kept, Frozen: kept}, nil
}

// scoredTeams counts the teams the caller has scored in the event.
func scoredTeams(ctx context.Context, db sqlkit.Querier, eventID, userID string) (*int, error) {
	_, scores, err := entryrepo.ScoreCoverage(ctx, db, eventID)
	n := scores[userID]
	return &n, err
}

// closeOptions says which ways the last organiser may close the event: it can only be ended once somebody has joined.
func closeOptions(ctx context.Context, db sqlkit.Querier, eventID string) (map[string]any, error) {
	participants, _, _, err := eventrepo.CountEventMembers(ctx, db, eventID)
	end := map[string]any{"allowed": true, "blocked": nil}
	if participants == 0 {
		end = map[string]any{"allowed": false, "blocked": "archive_needs_participants"}
	}
	return map[string]any{"end": end, "delete": map[string]any{"allowed": true}}, err
}

// organiserNames lists the other organisers' display names.
func organiserNames(others []model.OtherOrganiser) []string {
	names := make([]string, 0, len(others))
	for _, o := range others {
		names = append(names, o.DisplayName)
	}
	return names
}

// firstThree is at most the first three names, the most a dialog shows.
func firstThree(names []string) []string {
	return names[:min(3, len(names))]
}
