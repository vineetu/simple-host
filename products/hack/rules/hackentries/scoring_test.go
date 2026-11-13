package hackentries

import (
	"math"
	"testing"
)

func nearScore(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.02 {
		t.Fatalf("%s: got %.4f want %.4f", name, got, want)
	}
}

func TestJudgingNormalisationHandCalculated(t *testing.T) {
	entries := []JudgeEntry{{Team: "A", Judge: "J1", Raw: 100}, {Team: "B", Judge: "J1", Raw: 90}, {Team: "A", Judge: "J2", Raw: 0}, {Team: "B", Judge: "J2", Raw: 80}, {Team: "C", Judge: "J2", Raw: 100}}
	got := normalisedEntries(entries)
	// Global population mean=74, spread=sqrt(1424)=37.7359.
	// J1 mean/spread=95/5; J2=60/sqrt(1866.67).
	// Raw means A=50, B=85; normalised means A=66.67, B=63.87.
	nearScore(t, "A", (got[JudgeTeamKey("J1", "A")]+got[JudgeTeamKey("J2", "A")])/2, 66.66545)
	nearScore(t, "B", (got[JudgeTeamKey("J1", "B")]+got[JudgeTeamKey("J2", "B")])/2, 63.86621)
	if got[JudgeTeamKey("J1", "A")] <= got[JudgeTeamKey("J1", "B")] {
		t.Fatal("within-judge order reversed")
	}
	flat := normalisedEntries([]JudgeEntry{{Team: "A", Judge: "J1", Raw: 20}, {Team: "B", Judge: "J1", Raw: 20}, {Team: "A", Judge: "J2", Raw: 40}, {Team: "B", Judge: "J2", Raw: 40}})
	for _, score := range flat {
		nearScore(t, "zero judge spread fallback", score, 30)
	}
	zero := normalisedEntries([]JudgeEntry{{Team: "A", Judge: "J1", Raw: 50}, {Team: "B", Judge: "J1", Raw: 50}})
	for _, score := range zero {
		nearScore(t, "zero global spread fallback", score, 50)
		if math.IsNaN(score) {
			t.Fatal("NaN")
		}
	}
}
