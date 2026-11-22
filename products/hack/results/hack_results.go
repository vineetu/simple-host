package results

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *handler) publishResults(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		RankOverrides []struct {
			TeamID string `json:"team_id"`
			Rank   int    `json:"rank"`
		} `json:"rank_overrides"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	overrides := make([]entryapp.RankOverride, len(req.RankOverrides))
	for i, ov := range req.RankOverrides {
		overrides[i] = entryapp.RankOverride(ov)
	}
	move, committed := h.publishStageMove(r)
	rows, full, err := h.Entries.PublishResults(r.Context(), a.Event.ID, a.User.ID, overrides, move, h.Links.ResultSiteURL(r.Context(), a.Event), committed)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": rows, "full_ranking": full})
}

// publishStageMove is a publish's move to Results and the side effects that
// run once the move commits with the snapshot, even if the read after the
// commit fails.
func (h *handler) publishStageMove(r *http.Request) (func(*sql.Tx, model.Event) (model.Event, error), func()) {
	moved := false
	var ev model.Event
	var previous string
	move := func(tx *sql.Tx, locked model.Event) (model.Event, error) {
		updated, cur, err := h.Events.SetStageTx(r.Context(), tx, locked, "results")
		if err != nil {
			return locked, err
		}
		moved, ev, previous = true, updated, cur
		return updated, nil
	}
	committed := func() {
		core.PaperTimes(r)
		if !moved {
			return
		}
		if previous != ev.Stage {
			core.PaperStage(r, ev)
		}
		h.Links.PinDue(r.Context(), ev.ID)
		h.Links.RequestEventCert(ev)
	}
	return move, committed
}

func (h *handler) patchResults(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		FullRanking *bool `json:"full_ranking"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) || req.FullRanking == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "full_ranking is required")
		return
	}
	res, ok2, err := h.Entries.SetFullRanking(r.Context(), a.Event.ID, *req.FullRanking)
	if err != nil {
		log.Printf("hack: results %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	if !ok2 {
		httpx.WriteError(w, http.StatusNotFound, "no_results_yet", "Publish results first.")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"full_ranking": res.FullRanking, "results": json.RawMessage(res.Snapshot)})
}

func (h *handler) getPublicResults(w http.ResponseWriter, r *http.Request) {
	ev, err := h.Events.EventBySlug(r.Context(), strings.ToLower(strings.TrimSpace(r.PathValue("slug"))))
	if err == nil {
		core.PaperEvent(r, ev)
	}
	// A taken-down event's results are not programmatically readable, even
	// though its page still partially renders: a conservative choice for a
	// public, unauthenticated route.
	if err == nil && ev.TakenDown() {
		err = sql.ErrNoRows
	}
	var pub entryapp.Published
	if err == nil {
		pub, err = h.Entries.PublishedResults(r.Context(), ev.ID)
	}
	switch {
	case err != nil:
		core.WriteError(w, err)
	case !pub.Published:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"published": false})
	case h.viewerOrganises(r, ev):
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": pub.Snapshot, "full_ranking": pub.FullRanking})
	default:
		h.writePublicResults(w, r.Context(), ev, pub)
	}
}

func (h *handler) viewerOrganises(r *http.Request, ev model.Event) bool {
	u := auth.GetUser(r.Context())
	if u == nil {
		return false
	}
	m, err := h.Events.EventMember(r.Context(), ev.ID, u.ID)
	return u.IsAdmin || (err == nil && m.Role == "organiser")
}

func (h *handler) writePublicResults(w http.ResponseWriter, ctx context.Context, ev model.Event, pub entryapp.Published) {
	live := h.Links.LiveResultSites(ctx, h.Events, ev)
	rows := entryapp.PublicResultRows(ev, pub)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, publicResultJSON(row, ev, pub.FullRanking, live))
	}
	key := "winners"
	if pub.FullRanking {
		key = "ranking"
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{key: out})
}

// publicResultJSON is one row of the public results: places only in the
// full ranking and when the event shows them, scores when it shows them,
// and the card's site link from the sites live now, never the snapshot's.
func publicResultJSON(row entryapp.ResultRow, ev model.Event, full bool, live map[string]string) map[string]any {
	// Pass stored values through as saved: absent stays null, no zero values.
	get := func(key string, typed any) any {
		if row.Raw != nil {
			return row.Raw[key]
		}
		return typed
	}
	item := map[string]any{
		"team_id": row.TeamID, "team_name": row.TeamName, "track": get("track", row.Track), "track_winner": get("track_winner", row.TrackWinner),
		"deciding_mode": get("deciding_mode", row.DecidingMode), "overall_prize": entryapp.OverallPrizeLabel(*row.Rank, ev.OverallPrizes),
	}
	if full && ev.PublicRanks {
		item["rank"], item["track_rank"], item["tied"] = get("rank", row.Rank), get("track_rank", row.TrackRank), get("tied", row.Tied)
	}
	if ev.PublicScores {
		item["total"], item["raw_total"], item["normalised_total"] = get("total", row.Total), get("raw_total", row.RawTotal), get("normalised_total", row.NormalisedTotal)
	}
	if row.Raw != nil {
		if m, ok := row.Raw["entry"].(map[string]any); ok {
			card := make(map[string]any, len(m))
			for k, v := range m {
				card[k] = v
			}
			card["site_url"] = live[row.TeamID]
			item["entry"] = card
		}
	} else if row.Entry != nil {
		card := *row.Entry
		card.SiteURL = live[row.TeamID]
		item["entry"] = card
	}
	return item
}

func (h *handler) getMyResults(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := core.ParticipantTeam(w, r, h.Teams, a)
	if !ok {
		return
	}
	res, err := h.Entries.TeamResults(r.Context(), a.Event, team.ID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	if !res.Published {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"published": false})
		return
	}
	row := res.Row
	var entry *entryapp.EntryCard
	if res.ShownPublicly {
		entry = row.Entry
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"published": true, "comments": res.Comments, "ranked_teams": res.RankedTeams,
		"shown_publicly": res.ShownPublicly, "entry": entry,
		"total": row.Total, "raw_total": row.RawTotal, "normalised_total": row.NormalisedTotal,
		"deciding_mode": row.DecidingMode, "track": row.Track, "track_rank": row.TrackRank,
		"track_winner": row.TrackWinner, "rank": row.Rank, "tied": row.Tied,
	})
}

func (h *handler) exportScoresCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.Entries.ExportScores(r.Context(), a.Event.ID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	cells := [][]string{{"team", "judge", "criterion", "points", "max_points", "comment"}}
	for _, row := range rows {
		cells = append(cells, []string{row.TeamName, row.JudgeName, row.CriterionName, strconv.Itoa(row.Points), strconv.Itoa(row.MaxPoints), row.Comment})
	}
	csvAttachment(w, a.Event.Slug, "scores")
	core.WriteCSV(w, cells)
}

func (h *handler) exportResultsCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := h.Entries.ExportResults(r.Context(), a.Event)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	csvAttachment(w, a.Event.Slug, "results")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"team", "total", "rank", "tied", "judges_scored", "raw_total", "normalised_total", "deciding_mode", "track", "track_prize", "track_rank", "track_winner"})
	for _, res := range rows {
		var track model.EventTrack
		if res.Track != nil {
			track = *res.Track
		}
		_ = cw.Write([]string{httpx.CSVSafe(res.TeamName), csvPoints(res.Total), csvPlace(res.Rank), strconv.FormatBool(res.Tied), strconv.Itoa(res.JudgesScored),
			csvPoints(res.RawTotal), csvPoints(res.NormalisedTotal), res.DecidingMode, httpx.CSVSafe(track.Name), httpx.CSVSafe(track.Prize), csvPlace(res.TrackRank), strconv.FormatBool(res.TrackWinner)})
	}
	cw.Flush()
}

func csvAttachment(w http.ResponseWriter, eventSlug, kind string) {
	filename := fmt.Sprintf("%s-%s-%s.csv", eventSlug, kind, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
}

func csvPoints(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 2, 64)
}

func csvPlace(v *int) string {
	if v == nil {
		return ""
	}
	return strconv.Itoa(*v)
}
