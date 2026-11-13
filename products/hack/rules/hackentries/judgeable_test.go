package hackentries

import "testing"

func TestJudgeable(t *testing.T) {
	for _, c := range JudgeableCases {
		t.Run(c.Name, func(t *testing.T) {
			if got := Judgeable(c.Entry); got != c.Want {
				t.Fatalf("Judgeable = %v, want %v", got, c.Want)
			}
		})
	}
}
