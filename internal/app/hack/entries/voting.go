package entries

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
	repo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	"regexp"
	"time"
)

// VotingSettings reads the event's voting configuration.
func (s *Service) VotingSettings(ctx context.Context, id string) (repo.VotingSettings, error) {
	return repo.GetVotingSettings(ctx, s.db, id)
}

// IneligibleVotes counts votes that the eligibility rule would remove.
func (s *Service) IneligibleVotes(ctx context.Context, id, eligibility string) (int64, error) {
	return repo.IneligibleVotes(ctx, s.db, id, eligibility, false)
}

// VotingStatus evaluates the voting window.
func VotingStatus(ev db.Event, v repo.VotingSettings, now time.Time) string {
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
func (s *Service) SaveVotingSettings(ctx context.Context, id string, v repo.VotingSettings, confirm bool) (db.Event, repo.VotingSettings, int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.Event{}, v, 0, err
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

// VoteOptions lists eligible projects.
func (s *Service) VoteOptions(ctx context.Context, id, account string) ([]repo.VoteOption, error) {
	return repo.VoteOptions(ctx, s.db, id, account)
}

// VoteFinalRanking returns final vote totals.
func (s *Service) VoteFinalRanking(ctx context.Context, id, account string) ([]repo.VoteOption, error) {
	return repo.VoteFinalRanking(ctx, s.db, id, account)
}

// OwnVote returns the selected team.
func (s *Service) OwnVote(ctx context.Context, id, email string) (string, error) {
	return repo.OwnVote(ctx, s.db, id, email)
}

// VoterEligible checks the event's voter rule.
func (s *Service) VoterEligible(ctx context.Context, id, eligibility, email string) (bool, error) {
	return repo.VoterEligible(ctx, s.db, id, eligibility, email)
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
		return false, repo.ErrVoteClosed
	}
	allowed, err := repo.VoterEligible(ctx, tx, id, v.Eligibility, email)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, repo.ErrVoteIneligible
	}
	teamID, err := repo.VoteTeamID(ctx, tx, id, account, team)
	if errors.Is(err, sql.ErrNoRows) {
		return false, repo.ErrVoteTeamNotFound
	}
	if err != nil {
		return false, err
	}
	own, err := repo.VoteOwnTeam(ctx, tx, id, teamID, email)
	if err != nil {
		return false, err
	}
	if own {
		return false, repo.ErrVoteOwnTeam
	}
	created, err := repo.SaveVote(ctx, tx, id, email, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, repo.ErrVoteDuplicate
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

// Vote errors preserve the existing voting refusals.
var (
	ErrVoteClosed       = repo.ErrVoteClosed
	ErrVoteIneligible   = repo.ErrVoteIneligible
	ErrVoteOwnTeam      = repo.ErrVoteOwnTeam
	ErrVoteTeamNotFound = repo.ErrVoteTeamNotFound
	ErrVoteDuplicate    = repo.ErrVoteDuplicate
)

// VotingSettingsRead contains the configuration and removal counts for an organiser.
type VotingSettingsRead struct {
	Settings VotingConfig
	Status   string
	Counts   map[string]int64
}

// ReadVotingSettings composes the organiser's voting settings and counts.
func (s *Service) ReadVotingSettings(ctx context.Context, ev db.Event, now func() time.Time) (VotingSettingsRead, error) {
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

// ErrVotingEventNotFound indicates a missing or unavailable voting event.
var ErrVotingEventNotFound = errors.New("voting event not found")

// VotingEvent loads an event visible on the public voting routes.
func (s *Service) VotingEvent(ctx context.Context, slug string) (db.Event, error) {
	ev, err := eventrepo.GetEventBySlug(ctx, s.db, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, ErrVotingEventNotFound
	}
	if err != nil {
		return ev, err
	}
	if ev.Stage == "draft" || ev.TakenDown() {
		return ev, ErrVotingEventNotFound
	}
	return ev, nil
}

// PublicVote is the data shown on the public voting page.
type PublicVote struct {
	Event            db.Event
	Settings         VotingConfig
	Status           string
	Options, Ranking []repo.VoteOption
}

// ReadPublicVote collects voting options and, when closed, the final ranking.
func (s *Service) ReadPublicVote(ctx context.Context, ev db.Event, now func() time.Time) (PublicVote, error) {
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
func (s *Service) ReadMyVote(ctx context.Context, ev db.Event, username string, admin bool) (db.Event, string, bool, error) {
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
func ValidVoteTeam(team string) bool { return len(team) <= 30 && votingTeamSlug.MatchString(team) }

// CanChangeDirectory reports whether the organiser can change the directory flag.
func CanChangeDirectory(admin, joined bool) bool { return !admin || joined }
