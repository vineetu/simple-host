package entries

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
)

// ResultRow is one team's row of a published results snapshot; older rows have no Entry.
type ResultRow struct {
	TeamID          string            `json:"team_id"`
	TeamName        string            `json:"team_name"`
	Total           *float64          `json:"total"`
	Rank            *int              `json:"rank"`
	Tied            bool              `json:"tied"`
	JudgesScored    int               `json:"judges_scored"`
	RawTotal        *float64          `json:"raw_total"`
	NormalisedTotal *float64          `json:"normalised_total"`
	DecidingMode    string            `json:"deciding_mode"`
	TieDecider      string            `json:"tie_decider"`
	Track           *model.EventTrack `json:"track"`
	TrackRank       *int              `json:"track_rank"`
	TrackWinner     bool              `json:"track_winner"`
	Entry           *EntryCard        `json:"entry,omitempty"`

	// Raw is the row exactly as stored, set when read from a snapshot, so
	// old sparse rows can be passed on without gaining zero values.
	Raw map[string]any `json:"-"`
}

// EntryCard is a ranked team's public summary card in a results snapshot.
type EntryCard struct {
	Title         string            `json:"title"`
	Tagline       string            `json:"tagline"`
	Problem       string            `json:"problem"`
	Audience      string            `json:"audience"`
	Tools         []string          `json:"tools"`
	Maturity      string            `json:"maturity"`
	Track         *model.EventTrack `json:"track"`
	VideoURL      string            `json:"video_url"`
	VideoHost     string            `json:"video_host"`
	CodeURL       string            `json:"code_url"`
	CodeHost      string            `json:"code_host"`
	SiteURL       string            `json:"site_url"`
	HasScreenshot bool              `json:"has_screenshot"`
}

// SnapshotRows turns calculated results into snapshot rows, each ranked
// team carrying its card from cards (nil for a preview, which has none).
func SnapshotRows(results []TeamResult, cards map[string]EntryCard) []ResultRow {
	out := make([]ResultRow, 0, len(results))
	for _, res := range results {
		row := ResultRow{
			TeamID: res.TeamID, TeamName: res.TeamName, Total: res.Total, Rank: res.Rank, Tied: res.Tied,
			JudgesScored: res.JudgesScored, RawTotal: res.RawTotal, NormalisedTotal: res.NormalisedTotal,
			DecidingMode: res.ScoreMode, TieDecider: res.TieDecider,
			Track: res.Track, TrackRank: res.TrackRank, TrackWinner: res.TrackWinner,
		}
		if card, ok := cards[res.TeamID]; ok {
			row.Entry = &card
		}
		out = append(out, row)
	}
	return out
}

// PublishEventResults publishes results, moving Closed or Judging to Results in the same transaction.
func (s *Service) PublishEventResults(ctx context.Context, eventID, userID string, overrides []RankOverride,
	moveToResults func(*sql.Tx, model.Event) (model.Event, error),
	siteURL func(teamID, teamSlug string) (string, error), committed func()) ([]ResultRow, bool, error) {
	var ev model.Event
	var rows []ResultRow
	prepare := func(tx *sql.Tx, locked model.Event) (model.Event, error) {
		ev = locked
		move, err := publishMovesStage(locked.Stage)
		if err != nil || !move {
			return locked, err
		}
		ev, err = moveToResults(tx, locked)
		return ev, err
	}
	snapshot := func(q sqlkit.Querier, results []TeamResult) ([]byte, error) {
		cards, err := s.entryCards(ctx, q, ev, results, siteURL)
		if err != nil {
			return nil, fmt.Errorf("results cards: %w", err)
		}
		rows = SnapshotRows(results, cards)
		return json.Marshal(rows)
	}
	full, _, err := s.PublishResults(ctx, eventID, userID, overrides, prepare, snapshot, committed)
	return rows, full.FullRanking, err
}

// publishMovesStage reports whether a publish moves the stage to Results, refusing before entries close.
func publishMovesStage(stage string) (bool, error) {
	switch stage {
	case "draft", "open", "building":
		return false, &Error{Code: "judging_not_started", Message: "Close entries and judge them before publishing results."}
	case "results", "archived":
		return false, nil
	}
	return true, nil
}

// entryCards builds each ranked team's public summary card; unranked entries get none.
func (s *Service) entryCards(ctx context.Context, q sqlkit.Querier, ev model.Event, results []TeamResult, siteURL func(teamID, teamSlug string) (string, error)) (map[string]EntryCard, error) {
	if !slices.ContainsFunc(results, func(r TeamResult) bool { return r.Rank != nil }) {
		return nil, nil
	}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	slugByID := make(map[string]string, len(teams))
	for _, t := range teams {
		slugByID[t.ID] = t.Slug
	}
	list, err := entryrepo.ListEventEntries(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	entryByTeam := make(map[string]model.EventEntry, len(list))
	for _, e := range list {
		entryByTeam[e.TeamID] = e
	}
	codeOff := LoadRules(ev.EntryRules)["code_url"] == "off"
	out := make(map[string]EntryCard, len(results))
	for _, res := range results {
		if res.Rank == nil {
			continue
		}
		site, err := siteURL(res.TeamID, slugByID[res.TeamID])
		if err != nil {
			return nil, err
		}
		out[res.TeamID] = entryCard(entryByTeam[res.TeamID], res.Track, site, codeOff)
	}
	return out, nil
}

// entryCard is one entry's summary card.
func entryCard(e model.EventEntry, track *model.EventTrack, siteURL string, codeOff bool) EntryCard {
	codeURL := e.CodeURL
	if codeOff {
		codeURL = ""
	}
	tools := e.Tools
	if tools == nil {
		tools = []string{}
	}
	return EntryCard{
		Title: e.Title, Tagline: e.Tagline, Problem: e.Problem, Audience: e.Audience,
		Tools: tools, Maturity: e.Maturity, Track: track,
		VideoURL: e.VideoURL, VideoHost: linkHost(e.VideoURL),
		CodeURL: codeURL, CodeHost: linkHost(codeURL),
		SiteURL: siteURL, HasScreenshot: e.HasScreenshot,
	}
}

// linkHost is the host shown next to a link, "" for none or one that does
// not parse.
func linkHost(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Host
}

// Published is the event's published results, Published false before the
// first publish.
type Published struct {
	Published, FullRanking bool
	Rows                   []ResultRow
	Snapshot               json.RawMessage // the stored rows, untouched
}

// PublishedResults reads and decodes the published snapshot.
func (s *Service) PublishedResults(ctx context.Context, eventID string) (Published, error) {
	res, err := entryrepo.GetEventResults(ctx, s.db, eventID)
	if err != nil || !res.Published {
		return Published{}, err
	}
	out := Published{Published: true, FullRanking: res.FullRanking, Snapshot: res.Snapshot}
	var raws []json.RawMessage
	if err := json.Unmarshal(res.Snapshot, &raws); err != nil {
		return Published{}, fmt.Errorf("results snapshot: %w", err)
	}
	for _, raw := range raws {
		var row ResultRow
		// Old rows may hold fields of another type; keep what decodes and
		// never refuse the snapshot for it.
		var typeErr *json.UnmarshalTypeError
		if err := json.Unmarshal(raw, &row); err != nil && !errors.As(err, &typeErr) {
			return Published{}, fmt.Errorf("results snapshot: %w", err)
		}
		if err := json.Unmarshal(raw, &row.Raw); err != nil {
			return Published{}, fmt.Errorf("results snapshot: %w", err)
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// OverallPrizeLabel is the overall place label ("1st · Laptop") for ranks
// one to three that carry a prize, "" otherwise.
func OverallPrizeLabel(rank int, prizes []string) string {
	if rank < 1 || rank > 3 || rank > len(prizes) || prizes[rank-1] == "" {
		return ""
	}
	return []string{"1st", "2nd", "3rd"}[rank-1] + " · " + prizes[rank-1]
}

// ShownInWinnersOnly reports whether a ranked team appears when only the
// winners are public: first place, a place with a prize, or a track winner.
func ShownInWinnersOnly(rank int, trackWinner bool, prizes []string) bool {
	return rank > 0 && (rank == 1 || trackWinner || OverallPrizeLabel(rank, prizes) != "")
}

// shownPublicly reports whether a stranger sees this ranked row: all with full ranking, else winners only.
func (r ResultRow) shownPublicly(fullRanking bool, prizes []string) bool {
	return r.Rank != nil && (fullRanking || ShownInWinnersOnly(*r.Rank, r.TrackWinner, prizes))
}

// PublicResultRows picks the rows a stranger sees, sorted by team name when
// the event keeps its ranks private.
func PublicResultRows(ev model.Event, pub Published) []ResultRow {
	out := []ResultRow{}
	for _, row := range pub.Rows {
		if row.shownPublicly(pub.FullRanking, ev.OverallPrizes) {
			out = append(out, row)
		}
	}
	if !ev.PublicRanks {
		slices.SortStableFunc(out, func(a, b ResultRow) int { return strings.Compare(a.TeamName, b.TeamName) })
	}
	return out
}

// MyResults is a participant team's view of the published results.
type MyResults struct {
	Published bool
	Comments  []string
	// RankedTeams is how many teams got a place, the "of N" in "1st of 2
	// judged teams": teams with no entry or no scores have no place.
	RankedTeams int
	// ShownPublicly is whether a stranger sees the team's row; the team
	// always sees its own place regardless.
	ShownPublicly bool
	// Row is the team's snapshot row, or an empty row in the event's score
	// mode when the team is not in the snapshot.
	Row ResultRow
}

// TeamResults reads the team's row, the judges' comments to it (sorted)
// and how the public sees it.
func (s *Service) TeamResults(ctx context.Context, ev model.Event, teamID string) (MyResults, error) {
	pub, err := s.PublishedResults(ctx, ev.ID)
	if err != nil || !pub.Published {
		return MyResults{}, err
	}
	comments, err := s.TeamComments(ctx, ev.ID, teamID)
	if err != nil {
		return MyResults{}, err
	}
	slices.Sort(comments)
	out := MyResults{Published: true, Comments: comments, Row: ResultRow{DecidingMode: ev.ScoreMode}}
	for _, row := range pub.Rows {
		if row.Rank != nil {
			out.RankedTeams++
		}
	}
	if i := slices.IndexFunc(pub.Rows, func(r ResultRow) bool { return r.TeamID == teamID }); i >= 0 {
		out.Row = pub.Rows[i]
		out.ShownPublicly = out.Row.shownPublicly(pub.FullRanking, ev.OverallPrizes)
	}
	return out, nil
}

// ExportResults is the published snapshot's rows, or the live calculation before the first publish.
func (s *Service) ExportResults(ctx context.Context, ev model.Event) ([]ResultRow, error) {
	pub, err := s.PublishedResults(ctx, ev.ID)
	if err != nil || pub.Published {
		return pub.Rows, err
	}
	results, err := s.LiveResults(ctx, ev)
	return SnapshotRows(results, nil), err
}

// LiveResults is the current calculation from the stored scores.
func (s *Service) LiveResults(ctx context.Context, ev model.Event) ([]TeamResult, error) {
	return s.ComputeResults(ctx, s.db, ev)
}

// ExportScores is every raw score row in export order: team, judge (both
// case-insensitive), then criterion position.
func (s *Service) ExportScores(ctx context.Context, eventID string) ([]model.RawScoreRow, error) {
	rows, err := s.RawScores(ctx, eventID)
	slices.SortFunc(rows, func(a, b model.RawScoreRow) int {
		return cmp.Or(
			nameOrder(a.TeamName, b.TeamName),
			nameOrder(a.JudgeName, b.JudgeName),
			cmp.Compare(a.CriterionPosition, b.CriterionPosition))
	})
	return rows, err
}

// nameOrder compares names case-insensitively; names differing only in case
// sort as equal.
func nameOrder(a, b string) int {
	if a == b {
		return 0
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}
