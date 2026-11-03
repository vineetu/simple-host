package entries

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"time"

	"github.com/vsriram/simple-host/internal/db" // Host (4.3)
	model "github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
)

// VotingStatus evaluates the voting window.
func VotingStatus(ev model.Event, v repo.VotingSettings, now time.Time) string {
	if !v.Enabled || !ev.GalleryOpen || ev.Stage == "draft" || ev.TakenDown() || !v.OpensAt.Valid || !v.ClosesAt.Valid {
		return "disabled"
	}
	if !now.Before(v.ClosesAt.Time) {
		return "closed"
	}
	if ev.Stage == "archived" {
		return "disabled"
	}
	if now.Before(v.OpensAt.Time) {
		return "upcoming"
	}
	return "open"
}

// VoteRemovalConfirmation carries the count requiring confirmation.
type VoteRemovalConfirmation struct{ Count int64 }

func (e *VoteRemovalConfirmation) Error() string { return "Confirm removal of earlier votes." }

// SaveVotingSettings updates voting and removes ineligible votes under the event lock.
func (s *Service) SaveVotingSettings(ctx context.Context, id string, v repo.VotingSettings, confirm bool) (model.Event, repo.VotingSettings, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, v, 0, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, id)
	if err != nil {
		return ev, v, 0, err
	}
	if ev.Stage == "archived" {
		return ev, v, 0, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	old, err := repo.GetVotingSettings(ctx, tx, id)
	if err != nil {
		return ev, v, 0, err
	}
	if v.Enabled && !ev.GalleryOpen {
		return ev, v, 0, &Error{Code: "gallery_closed", Message: "open the project gallery before opening voting"}
	}
	var removed int64
	if old.Eligibility != v.Eligibility {
		count, err := repo.IneligibleVotes(ctx, tx, id, v.Eligibility, false)
		if err != nil {
			return ev, v, 0, err
		}
		if count > 0 && !confirm {
			return ev, v, 0, &VoteRemovalConfirmation{Count: count}
		}
		removed, err = repo.IneligibleVotes(ctx, tx, id, v.Eligibility, true)
		if err != nil {
			return ev, v, 0, err
		}
	}
	v.DirectoryListed = old.DirectoryListed
	if err = repo.SaveVotingSettings(ctx, tx, id, v); err != nil {
		return ev, v, 0, err
	}
	if err = tx.Commit(); err != nil {
		return ev, v, 0, err
	}
	return ev, v, removed, nil
}

// SetDirectoryListed changes the event's directory flag.
func (s *Service) SetDirectoryListed(ctx context.Context, id string, listed bool) error {
	return repo.SetDirectoryListed(ctx, s.db, id, listed)
}

// CastVote records a vote under the event lock.
func (s *Service) CastVote(ctx context.Context, id, account, email, team string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	v, err := repo.LockVoteWindow(ctx, tx, id)
	if err != nil {
		return false, err
	}
	now, err := repo.VoteClock(ctx, tx)
	if err != nil {
		return false, err
	}
	if !v.Enabled || !v.GalleryOpen || !v.Opens.Valid || !v.Closes.Valid || now.Before(v.Opens.Time) || !now.Before(v.Closes.Time) || v.Stage == "draft" || v.Stage == "archived" || v.TakenDown.Valid {
		return false, &Error{Code: "voting_closed", Message: "voting is not open"}
	}
	allowed, err := repo.VoterEligible(ctx, tx, id, v.Eligibility, email)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, &Error{Code: "vote_ineligible", Message: "this account is not eligible to vote"}
	}
	teamID, err := repo.VoteTeamID(ctx, tx, id, account, team)
	if errors.Is(err, sql.ErrNoRows) {
		return false, &Error{Code: "team_not_found", Message: "choose a live team project"}
	}
	if err != nil {
		return false, err
	}
	own, err := repo.VoteOwnTeam(ctx, tx, id, teamID, email)
	if err != nil {
		return false, err
	}
	if own {
		return false, &Error{Code: "own_team_vote", Message: "you cannot vote for your own team"}
	}
	created, err := repo.SaveVote(ctx, tx, id, email, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, &Error{Code: "duplicate_vote", Message: "you already voted for this team; choose another to change your vote"}
	}
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return created, nil
}

// VotingSettingsValue constructs a configuration from transport inputs.
func VotingSettingsValue(enabled bool, opens, closes sql.NullTime, eligibility string) repo.VotingSettings {
	return repo.VotingSettings{Enabled: enabled, OpensAt: opens, ClosesAt: closes, Eligibility: eligibility}
}

// VotingConfig is the stored voting configuration.
type VotingConfig = repo.VotingSettings

// VotingSettingsRead contains the configuration and removal counts for an organiser.
type VotingSettingsRead struct {
	Settings VotingConfig
	Status   string
	Counts   map[string]int64
}

// ReadVotingSettings composes the organiser's voting settings and counts.
func (s *Service) ReadVotingSettings(ctx context.Context, ev model.Event, now func() time.Time) (VotingSettingsRead, error) {
	var out VotingSettingsRead
	v, err := repo.GetVotingSettings(ctx, s.db, ev.ID)
	if err != nil {
		return out, err
	}
	out.Settings, out.Status = v, VotingStatus(ev, v, now())
	out.Counts = map[string]int64{}
	for _, eligibility := range []string{"all_signed_in", "event_members", "participants"} {
		count, err := repo.IneligibleVotes(ctx, s.db, ev.ID, eligibility, false)
		if err != nil {
			return out, err
		}
		out.Counts[eligibility] = count
	}
	return out, nil
}

// VotingEvent loads an event visible on the public voting routes. Loaded is true
// even for a hidden event, but false for a failed database scan.
func (s *Service) VotingEvent(ctx context.Context, slug string) (ev model.Event, loaded bool, err error) {
	ev, err = eventrepo.GetEventBySlug(ctx, s.db, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, false, &Error{Code: "event_not_found", Message: "voting event not found"}
	}
	if err != nil {
		return ev, false, err
	}
	if ev.Stage == "draft" || ev.TakenDown() {
		return ev, true, &Error{Code: "event_not_found", Message: "voting event not found"}
	}
	return ev, true, nil
}

// PublicVote is the data shown on the public voting page.
type PublicVote struct {
	Event            model.Event
	Settings         VotingConfig
	Status           string
	Options, Ranking []repo.VoteOption
}

// ReadPublicVote collects voting options and, when closed, the final ranking.
func (s *Service) ReadPublicVote(ctx context.Context, ev model.Event, now func() time.Time) (PublicVote, error) {
	out := PublicVote{Event: ev}
	var err error
	out.Settings, err = repo.GetVotingSettings(ctx, s.db, out.Event.ID)
	if err != nil {
		return out, err
	}
	out.Status = VotingStatus(out.Event, out.Settings, now())
	if out.Status != "disabled" {
		out.Options, err = repo.VoteOptions(ctx, s.db, out.Event.ID, out.Event.AccountID)
		if err != nil {
			return out, err
		}
		if out.Status == "closed" {
			out.Ranking, err = repo.VoteFinalRanking(ctx, s.db, out.Event.ID, out.Event.AccountID)
		}
	}
	return out, err
}

// ReadMyVote returns the selection and eligibility for a signed-in visitor.
func (s *Service) ReadMyVote(ctx context.Context, ev model.Event, username string, admin bool) (model.Event, string, bool, error) {
	email := db.BaseEmail(username)
	selected, err := repo.OwnVote(ctx, s.db, ev.ID, email)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ev, "", false, err
	}
	eligible := !admin && username != ""
	if eligible {
		v, err := repo.GetVotingSettings(ctx, s.db, ev.ID)
		if err != nil {
			return ev, "", false, err
		}
		eligible, err = repo.VoterEligible(ctx, s.db, ev.ID, v.Eligibility, email)
		if err != nil {
			return ev, "", false, err
		}
	}
	return ev, selected, eligible, nil
}

// ValidateVotingEligibility checks the requested voter rule before parsing the window.
func ValidateVotingEligibility(eligibility string) error {
	if eligibility != "all_signed_in" && eligibility != "event_members" && eligibility != "participants" {
		return &Error{Code: "invalid_voting_eligibility", Message: "choose all_signed_in, event_members or participants"}
	}
	return nil
}

// ValidateVotingWindow checks the parsed voting window.
func ValidateVotingWindow(v VotingConfig) error {
	if v.Enabled && (!v.OpensAt.Valid || !v.ClosesAt.Valid || !v.ClosesAt.Time.After(v.OpensAt.Time)) {
		return &Error{Code: "invalid_voting_window", Message: "opening and closing times are required, with closing after opening"}
	}
	return nil
}

// CanCastVote reports whether the account can use the voting endpoint.
func CanCastVote(username string, admin bool) bool { return !admin && username != "" }

var votingTeamSlug = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// ValidVoteTeam checks the team address supplied on the voting page.
func ValidVoteTeam(team string) bool { return ValidVotingLabel(team, 30) }

// ValidVotingLabel checks an event or team address label.
func ValidVotingLabel(label string, max int) bool {
	return len(label) <= max && votingTeamSlug.MatchString(label)
}

// CanChangeDirectory reports whether the organiser can change the directory flag.
func CanChangeDirectory(admin, joined bool) bool { return !admin || joined }
