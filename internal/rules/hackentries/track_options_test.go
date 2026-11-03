package hackentries

import (
	"github.com/vsriram/simple-host/internal/model"
	"testing"
)

func TestTrackRanksKeepResolvedTieGroupsSeparate(t *testing.T) {
	track := &model.EventTrack{ID: "track-a"}
	score := 80.0
	one, two := 1, 2
	rows := []TeamResult[model.EventTrack]{
		{TeamID: "A", Total: &score, Rank: &one, Tied: true, Track: track},
		{TeamID: "B", Total: &score, Rank: &one, Tied: true, Track: track},
		{TeamID: "C", Total: &score, Rank: &two, Tied: true, Track: track, TieDecider: "criterion"},
		{TeamID: "D", Total: &score, Rank: &two, Tied: true, Track: track},
	}
	MarkTrackWinners(rows, map[string]string{"A": "track-a", "B": "track-a", "C": "track-a", "D": "track-a"})
	for i, want := range []int{1, 1, 2, 2} {
		if rows[i].TrackRank == nil || *rows[i].TrackRank != want || rows[i].TrackWinner != (want == 1) {
			t.Fatalf("track rank %d: got %+v, want %d", i, rows[i], want)
		}
	}
	if err := ApplyRankOverrides(rows, []RankOverride{{TeamID: "C", Rank: 2}, {TeamID: "D", Rank: 1}}, map[string]string{"A": "track-a", "B": "track-a", "C": "track-a", "D": "track-a"}); err != nil {
		t.Fatal(err)
	}
	if rows[0].TeamID != "A" || rows[1].TeamID != "B" || rows[2].TeamID != "D" || rows[3].TeamID != "C" || rows[3].TieDecider != "organiser" || rows[0].TrackRank == nil || *rows[0].TrackRank != 1 || rows[2].TrackRank == nil || *rows[2].TrackRank != 2 {
		t.Fatalf("override reordered a previously resolved group: %+v", rows)
	}
}

func TestOrganiserOverridePreservesAutomaticTieDeciders(t *testing.T) {
	score := 75.0
	one, two, three := 1, 2, 3
	rows := []TeamResult[model.EventTrack]{
		{TeamID: "A", Total: &score, Rank: &one, TieDecider: "score"},
		{TeamID: "B", Total: &score, Rank: &two, TieDecider: "criterion"},
		{TeamID: "C", Total: &score, Rank: &three, Tied: true, TieDecider: "unresolved"},
		{TeamID: "D", Total: &score, Rank: &three, Tied: true, TieDecider: "unresolved"},
	}
	if err := ApplyRankOverrides(rows, []RankOverride{{TeamID: "C", Rank: 2}, {TeamID: "D", Rank: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if rows[0].TeamID != "A" || rows[1].TeamID != "B" || rows[1].TieDecider != "criterion" || rows[2].TeamID != "D" || rows[3].TeamID != "C" || rows[2].TieDecider != "organiser" || rows[3].TieDecider != "organiser" {
		t.Fatalf("organiser override disturbed decided ordering: %+v", rows)
	}
}
