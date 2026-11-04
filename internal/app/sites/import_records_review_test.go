package sites

import (
	"fmt"
	"testing"
	"time"
)

func TestImportRecordsPreserveLiveLinksAndEvictFinished(t *testing.T) {
	for _, state := range []string{"waiting", "receiving", "converting", "publishing", "queued"} {
		t.Run(state, func(t *testing.T) {
			m := NewImportManager(5)
			now := time.Now()
			expires := now.Add(-time.Hour)
			started := now
			if state == "waiting" {
				expires = now.Add(time.Hour)
				started = time.Time{}
			}
			m.Records["live"] = &ImportRecord{Owner: "owner", Created: now.Add(-time.Hour), Expires: expires, Started: started, State: state}
			for i := 0; i < 19; i++ {
				m.Records[fmt.Sprint(i)] = &ImportRecord{Owner: "owner", Created: now, Finished: now.Add(time.Duration(i)*time.Minute - 20*time.Minute), State: "done"}
			}
			if err := m.Created(ImportLink{ID: "new", Owner: "owner", Expires: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if m.Records["live"] == nil || m.Records["0"] != nil || len(m.Records) != 20 {
				t.Fatal("wrong report evicted")
			}
		})
	}
}
func TestImportRecordsSweepExpiredUnused(t *testing.T) {
	m := NewImportManager(5)
	past := time.Now().Add(-time.Hour)
	for _, state := range []string{"waiting", "receiving", "converting", "publishing"} {
		started := time.Time{}
		if state != "waiting" {
			started = time.Now()
		}
		m.Records[state] = &ImportRecord{State: state, Expires: past, Started: started}
	}
	m.SweepLocked()
	if m.Records["waiting"] != nil || len(m.Records) != 3 {
		t.Fatal("expired unused record retained or active job removed")
	}
}
