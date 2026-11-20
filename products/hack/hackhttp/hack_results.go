package hackhttp

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

type teamResult = entryapp.TeamResult

type rankOverride = entryapp.RankOverride

func (h *judgingHTTP) publishResults(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		RankOverrides []struct {
			TeamID string `json:"team_id"`
			Rank   int    `json:"rank"`
		} `json:"rank_overrides"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	// Publishing implies judging is finished (SIM-271). Before entries
	// close there is nothing to judge, so it is refused. From Closed or
	// Judging the event moves to Results first, the same transition the
	// Overview's "Finish judging" performs (it locks judging too), inside
	// the publish transaction and under the event lock: a refused or failed
	// publish leaves the stage as it was, and a concurrent stage change
	// cannot slip in between the check and the move. An archived event is
	// never moved; results can still be republished after End event.
	moved := false
	var current string
	prepare := func(tx *sql.Tx, ev model.Event) (model.Event, error) {
		switch ev.Stage {
		case "draft", "open", "building":
			return ev, errJudgingNotStarted
		case "results", "archived":
			return ev, nil
		}
		updated, cur, err := h.events.SetStageTx(r.Context(), tx, ev, "results", h.entries.SettleEventTx)
		if err != nil {
			return ev, err
		}
		moved, current = true, cur
		a.event = updated
		return updated, nil
	}
	overrides := make([]rankOverride, len(req.RankOverrides))
	for i, ov := range req.RankOverrides {
		overrides[i] = rankOverride{TeamID: ov.TeamID, Rank: ov.Rank}
	}
	var snapshot []map[string]any
	cardsFailed := false
	full, phase, err := h.entries.PublishResults(r.Context(), a.event.ID, a.user.ID, overrides, prepare, func(q entryapp.Querier, results []teamResult) ([]byte, error) {
		cards, err := h.resultsEntryCards(r.Context(), q, a.event, results)
		if err != nil {
			cardsFailed = true
			return nil, err
		}
		snapshot = resultsSnapshotJSON(results, cards)
		return json.Marshal(snapshot)
	}, func() {
		paperHackTimes(r)
		// The stage move committed with the snapshot: its side effects run
		// now, even if the read that follows the commit fails.
		if moved {
			if current != a.event.Stage {
				paperHackStage(r, a.event)
			}
			h.pinDue(r.Context(), a.event.ID)
			h.requestEventCert(a.event)
		}
	})
	if err != nil {
		if errors.Is(err, errJudgingNotStarted) {
			httpx.WriteError(w, http.StatusConflict, "judging_not_started", "Close entries and judge them before publishing results.")
			return
		}
		if _, ok := err.(*entryapp.Error); ok || phase == "prepare" {
			writeHackError(w, err)
			return
		}
		if cardsFailed {
			log.Printf("hack: publish %s: cards: %v", a.event.Slug, err)
		} else if phase == "compute" || phase == "write" || phase == "read" {
			log.Printf("hack: publish %s: %v", a.event.Slug, err)
		}
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": snapshot, "full_ranking": full.FullRanking})
}

// errJudgingNotStarted: publishing results before entries close (SIM-271).
var errJudgingNotStarted = errors.New("judging not started")

// resultsSnapshotJSON is the array stored as the published results snapshot
// and returned by POST .../results/publish (build-plan §4.6). Each ranked
// team's row also carries its public summary card under "entry" — the same
// card every ranked team gets, keyed implicitly by the row's own "team_id"
// rather than a separate top-level map, so the snapshot stays the plain
// array every existing reader (getPublicResults, getMyResults,
// hackResultsFromSnapshot) already unmarshals with `[]map[string]any`. A
// snapshot published before this slice simply has no "entry" key on any
// row, which every reader already treats as "no card" (judgement call;
// documented in the slice 3 report).
func resultsSnapshotJSON(results []teamResult, cards map[string]map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, res := range results {
		var total, rank any
		if res.Total != nil {
			total = *res.Total
		}
		if res.Rank != nil {
			rank = *res.Rank
		}
		row := map[string]any{
			"team_id":          res.TeamID,
			"team_name":        res.TeamName,
			"total":            total,
			"rank":             rank,
			"tied":             res.Tied,
			"judges_scored":    res.JudgesScored,
			"raw_total":        res.RawTotal,
			"normalised_total": res.NormalisedTotal,
			"deciding_mode":    res.ScoreMode,
			"tie_decider":      res.TieDecider,
			"track":            res.Track,
			"track_rank":       res.TrackRank,
			"track_winner":     res.TrackWinner,
		}
		if card, ok := cards[res.TeamID]; ok {
			row["entry"] = card
		}
		out = append(out, row)
	}
	return out
}

// resultsEntryCards builds every ranked team's public summary card for a
// results snapshot (build-plan §4.6): title, tagline, problem, audience,
// tools, maturity, track, the video link and its host, the code link
// (omitted -- left "" -- when the event's code rule is Off), the site link
// (only when the team's site is currently live and not taken down; a
// passcode or named-viewer site still gets its public URL, since the
// visitor just meets the gate there, never a preview link) and whether a
// screenshot is stored. Only ranked teams need a card: an excluded or never-
// scored entry has no Rank and so is never looked up here, which is how
// "excluded entries get no card" falls out for free -- EntryJudgeableSQL
// already kept them out of every judge's eligible set, so they carry no
// score and computeResultsWithOptions never ranks them.
func (h *judgingHTTP) resultsEntryCards(ctx context.Context, q entryapp.Querier, ev model.Event, results []teamResult) (map[string]map[string]any, error) {
	ranked := false
	for _, res := range results {
		if res.Rank != nil {
			ranked = true
			break
		}
	}
	if !ranked {
		return nil, nil
	}
	teams, err := h.entries.TeamsWithMembersInTx(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	slugByID := make(map[string]string, len(teams))
	for _, t := range teams {
		slugByID[t.ID] = t.Slug
	}
	entries, err := h.entries.EventEntriesInTx(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	entryByTeam := make(map[string]model.EventEntry, len(entries))
	for _, e := range entries {
		entryByTeam[e.TeamID] = e
	}
	tl, err := h.loadTeamList(ctx, ev)
	if err != nil {
		return nil, err
	}
	fields := entryapp.LoadRules(ev.EntryRules)
	codeOff := fields["code_url"] == "off"
	out := make(map[string]map[string]any, len(results))
	for _, res := range results {
		if res.Rank == nil {
			continue
		}
		e := entryByTeam[res.TeamID]
		slug := slugByID[res.TeamID]
		site := tl.site(slug)
		st := tl.states[res.TeamID]
		siteURL := ""
		if entryapp.TeamSiteLive(site, st) {
			siteURL = h.teamSiteURL(ev.Slug, slug)
		}
		videoHost, codeHost := "", ""
		if e.VideoURL != "" {
			videoHost = hackURLHost(e.VideoURL)
		}
		codeURL := e.CodeURL
		if codeOff {
			codeURL = ""
		}
		if codeURL != "" {
			codeHost = hackURLHost(codeURL)
		}
		out[res.TeamID] = map[string]any{
			"title": e.Title, "tagline": e.Tagline, "problem": e.Problem, "audience": e.Audience,
			"tools": nonNilStrings(e.Tools), "maturity": e.Maturity, "track": res.Track,
			"video_url": e.VideoURL, "video_host": videoHost,
			"code_url": codeURL, "code_host": codeHost,
			"site_url": siteURL, "has_screenshot": e.HasScreenshot,
		}
	}
	return out, nil
}

func (h *judgingHTTP) patchResults(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		FullRanking *bool `json:"full_ranking"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) || req.FullRanking == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "full_ranking is required")
		return
	}
	res, ok2, err := h.entries.SetFullRanking(r.Context(), a.event.ID, *req.FullRanking)
	if err != nil {
		log.Printf("hack: results %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	if !ok2 {
		httpx.WriteError(w, http.StatusNotFound, "no_results_yet", "Publish results first.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"full_ranking": res.FullRanking, "results": json.RawMessage(res.Snapshot)})
}

func (h *judgingHTTP) getPublicResults(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := h.events.EventBySlug(r.Context(), slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// A taken-down event's results are not programmatically readable, even
	// though its page still partially renders (410) — a conservative choice
	// for a public, unauthenticated route.
	if ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	res, err := h.entries.GetResults(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if !res.Published {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"published": false})
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal(res.Snapshot, &rows); err != nil {
		httpx.WriteInternal(w)
		return
	}
	// The organiser (and the platform admin) sees the same full snapshot
	// `publishResults` returns — every team, with total and judges_scored —
	// regardless of the public full_ranking switch, so the Judging tab can
	// redraw its own view correctly after a page reload. A missing key is the
	// public view.
	if u := auth.GetUser(r.Context()); u != nil {
		if m, merr := h.events.EventMember(r.Context(), ev.ID, u.ID); u.IsAdmin || (merr == nil && m.Role == "organiser") {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": rows, "full_ranking": res.FullRanking})
			return
		}
	}
	live := h.liveResultSites(r.Context(), ev)
	if !res.FullRanking {
		winners := make([]map[string]any, 0, 1)
		for _, row := range rows {
			rank, _ := row["rank"].(float64)
			if shownInWinnersOnly(int(rank), row["track_winner"] == true, ev.OverallPrizes) {
				item := map[string]any{"team_id": row["team_id"], "team_name": row["team_name"], "track": row["track"], "track_winner": row["track_winner"], "deciding_mode": row["deciding_mode"], "overall_prize": overallPrizeLabel(int(rank), ev.OverallPrizes)}
				if ev.PublicScores {
					item["total"] = row["total"]
					item["raw_total"] = row["raw_total"]
					item["normalised_total"] = row["normalised_total"]
				}
				if row["entry"] != nil {
					item["entry"] = publicResultEntry(row["entry"], row["team_id"], live)
				}
				winners = append(winners, item)
			}
		}
		if !ev.PublicRanks {
			sort.Slice(winners, func(i, j int) bool {
				return winners[i]["team_name"].(string) < winners[j]["team_name"].(string)
			})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"winners": winners})
		return
	}
	ranking := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row["rank"] == nil {
			continue
		}
		item := map[string]any{
			"team_id": row["team_id"], "team_name": row["team_name"],
			"track": row["track"], "track_winner": row["track_winner"], "deciding_mode": row["deciding_mode"],
		}
		rank, _ := row["rank"].(float64)
		item["overall_prize"] = overallPrizeLabel(int(rank), ev.OverallPrizes)
		if ev.PublicRanks {
			item["rank"] = row["rank"]
			item["track_rank"] = row["track_rank"]
			item["tied"] = row["tied"]
		}
		if ev.PublicScores {
			item["total"] = row["total"]
			item["raw_total"] = row["raw_total"]
			item["normalised_total"] = row["normalised_total"]
		}
		if row["entry"] != nil {
			item["entry"] = publicResultEntry(row["entry"], row["team_id"], live)
		}
		ranking = append(ranking, item)
	}
	if !ev.PublicRanks {
		sort.Slice(ranking, func(i, j int) bool {
			return ranking[i]["team_name"].(string) < ranking[j]["team_name"].(string)
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ranking": ranking})
}

func (h *judgingHTTP) getMyResults(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	res, err := h.entries.GetResults(r.Context(), a.event.ID)
	if err != nil {
		log.Printf("hack: my-results %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	if !res.Published {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"published": false})
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal(res.Snapshot, &rows); err != nil {
		httpx.WriteInternal(w)
		return
	}
	var mine map[string]any
	for _, row := range rows {
		if row["team_id"] == team.ID {
			mine = row
			break
		}
	}
	comments, err := h.entries.TeamComments(r.Context(), a.event.ID, team.ID)
	if err != nil {
		log.Printf("hack: my-results %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	sort.Strings(comments)
	// ranked_teams is how many teams got a place, the "of N" in "1st of 2
	// judged teams": teams with no entry or no scores have no place, so the
	// event's team count would overstate it (full-test fix, 2026-11-02).
	ranked := 0
	for _, row := range rows {
		if row["rank"] != nil {
			ranked++
		}
	}
	out := map[string]any{"published": true, "comments": comments, "ranked_teams": ranked}
	// shownPublicly and entry (build-plan §9 point 5, participant-results.js
	// "Your entry as the public sees it"): the same visibility the public
	// page and /results already apply -- full ranking shows every ranked
	// row, winners-only shows rank 1, places with a prize, or a track winner, and an
	// unranked (unscored or excluded) team is never shown either way. The
	// team always sees its own rank/score above regardless; this flag is
	// only about what a STRANGER sees, so the screen can say so plainly.
	shownPublicly := false
	if mine != nil && mine["rank"] != nil {
		rank, _ := mine["rank"].(float64)
		trackWinner, _ := mine["track_winner"].(bool)
		shownPublicly = res.FullRanking || shownInWinnersOnly(int(rank), trackWinner, a.event.OverallPrizes)
	}
	out["shown_publicly"] = shownPublicly
	if shownPublicly && mine != nil {
		out["entry"] = mine["entry"]
	} else {
		out["entry"] = nil
	}
	if mine != nil {
		out["total"] = mine["total"]
		out["raw_total"] = mine["raw_total"]
		out["normalised_total"] = mine["normalised_total"]
		out["deciding_mode"] = mine["deciding_mode"]
		out["track"] = mine["track"]
		out["track_rank"] = mine["track_rank"]
		out["track_winner"] = mine["track_winner"]
		out["rank"] = mine["rank"]
		out["tied"] = mine["tied"]
	} else {
		out["total"] = nil
		out["raw_total"] = nil
		out["normalised_total"] = nil
		out["deciding_mode"] = a.event.ScoreMode
		out["track"] = nil
		out["track_rank"] = nil
		out["track_winner"] = false
		out["rank"] = nil
		out["tied"] = false
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *judgingHTTP) exportScoresCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.entries.RawScores(r.Context(), a.event.ID)
	if err != nil {
		log.Printf("hack: export scores %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TeamName != rows[j].TeamName {
			return strings.ToLower(rows[i].TeamName) < strings.ToLower(rows[j].TeamName)
		}
		if rows[i].JudgeName != rows[j].JudgeName {
			return strings.ToLower(rows[i].JudgeName) < strings.ToLower(rows[j].JudgeName)
		}
		return rows[i].CriterionPosition < rows[j].CriterionPosition
	})
	filename := fmt.Sprintf("%s-scores-%s.csv", a.event.Slug, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"team", "judge", "criterion", "points", "max_points", "comment"})
	for _, row := range rows {
		_ = cw.Write([]string{
			httpx.CSVSafe(row.TeamName), httpx.CSVSafe(row.JudgeName), httpx.CSVSafe(row.CriterionName),
			strconv.Itoa(row.Points), strconv.Itoa(row.MaxPoints), httpx.CSVSafe(row.Comment),
		})
	}
	cw.Flush()
}

func (h *judgingHTTP) exportResultsCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	// Prefer the published, tie-resolved snapshot so this matches exactly
	// what teams and the public already see; only compute live when nothing
	// has been published yet (so the organiser can still get a working
	// export before the first publish).
	type row struct {
		TeamName        string
		Total           *float64
		RawTotal        *float64
		NormalisedTotal *float64
		DecidingMode    string
		TrackName       string
		TrackPrize      string
		TrackRank       *int
		TrackWinner     bool
		Rank            *int
		Tied            bool
		JudgesScored    int
	}
	var rows []row
	published, perr := h.entries.GetResults(r.Context(), a.event.ID)
	if perr != nil {
		log.Printf("hack: export results %s: %v", a.event.Slug, perr)
		httpx.WriteInternal(w)
		return
	}
	if published.Published {
		var snap []map[string]any
		if err := json.Unmarshal(published.Snapshot, &snap); err != nil {
			log.Printf("hack: export results %s: snapshot: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
			return
		}
		for _, m := range snap {
			rw := row{JudgesScored: int(jsonFloat(m["judges_scored"]))}
			if name, ok := m["team_name"].(string); ok {
				rw.TeamName = name
			}
			if t, ok := m["total"].(float64); ok {
				rw.Total = &t
			}
			if v, ok := m["raw_total"].(float64); ok {
				rw.RawTotal = &v
			}
			if v, ok := m["normalised_total"].(float64); ok {
				rw.NormalisedTotal = &v
			}
			if v, ok := m["deciding_mode"].(string); ok {
				rw.DecidingMode = v
			}
			if v, ok := m["track"].(map[string]any); ok {
				rw.TrackName, _ = v["name"].(string)
				rw.TrackPrize, _ = v["prize"].(string)
			}
			if v, ok := m["track_rank"].(float64); ok {
				n := int(v)
				rw.TrackRank = &n
			}
			rw.TrackWinner, _ = m["track_winner"].(bool)
			if rk, ok := m["rank"].(float64); ok {
				n := int(rk)
				rw.Rank = &n
			}
			if tied, ok := m["tied"].(bool); ok {
				rw.Tied = tied
			}
			rows = append(rows, rw)
		}
	} else {
		results, err := computeResultsWithOptions(r.Context(), h.database, a.event)
		if err != nil {
			log.Printf("hack: export results %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
			return
		}
		for _, res := range results {
			rw := row{TeamName: res.TeamName, Total: res.Total, RawTotal: res.RawTotal, NormalisedTotal: res.NormalisedTotal, DecidingMode: res.ScoreMode, TrackRank: res.TrackRank, TrackWinner: res.TrackWinner, Rank: res.Rank, Tied: res.Tied, JudgesScored: res.JudgesScored}
			if res.Track != nil {
				rw.TrackName = res.Track.Name
				rw.TrackPrize = res.Track.Prize
			}
			rows = append(rows, rw)
		}
	}
	filename := fmt.Sprintf("%s-results-%s.csv", a.event.Slug, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"team", "total", "rank", "tied", "judges_scored", "raw_total", "normalised_total", "deciding_mode", "track", "track_prize", "track_rank", "track_winner"})
	for _, res := range rows {
		total, rank := "", ""
		if res.Total != nil {
			total = strconv.FormatFloat(*res.Total, 'f', 2, 64)
		}
		if res.Rank != nil {
			rank = strconv.Itoa(*res.Rank)
		}
		rawTotal, normTotal, trackRank := "", "", ""
		if res.RawTotal != nil {
			rawTotal = strconv.FormatFloat(*res.RawTotal, 'f', 2, 64)
		}
		if res.NormalisedTotal != nil {
			normTotal = strconv.FormatFloat(*res.NormalisedTotal, 'f', 2, 64)
		}
		if res.TrackRank != nil {
			trackRank = strconv.Itoa(*res.TrackRank)
		}
		_ = cw.Write([]string{httpx.CSVSafe(res.TeamName), total, rank, strconv.FormatBool(res.Tied), strconv.Itoa(res.JudgesScored), rawTotal, normTotal, res.DecidingMode, httpx.CSVSafe(res.TrackName), httpx.CSVSafe(res.TrackPrize), trackRank, strconv.FormatBool(res.TrackWinner)})
	}
	cw.Flush()
}

// jsonFloat reads a JSON-decoded number (always float64 via encoding/json's
// map[string]any) or 0 for anything else (missing, null, wrong type).
func jsonFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

// liveResultSites maps team id to the public address of every team site that
// is live right now (the gallery's own query), so the public result routes
// (/results, /public) drop a card's site link once a site is taken down or
// goes offline after publish, without a republish -- as the event page does.
func (h *hackBase) liveResultSites(ctx context.Context, ev model.Event) map[string]string {
	out := map[string]string{}
	if h.sites == nil {
		return out
	}
	cards, err := h.events.GalleryCards(ctx, ev.ID, ev.AccountID)
	if err != nil {
		log.Printf("hack: result sites %s: %v", ev.Slug, err)
		return out
	}
	for _, c := range cards {
		if link := hackGalleryLink(h.sites.TeamSiteURL, ev.Slug, c.Slug); link != "" {
			out[c.ID] = link
		}
	}
	return out
}

// publicResultEntry is a snapshot row's card for a public route: a copy with
// site_url set from the live sites, never the snapshot's own.
func publicResultEntry(entry any, teamID any, live map[string]string) any {
	m, ok := entry.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	id, _ := teamID.(string)
	out["site_url"] = live[id]
	return out
}
