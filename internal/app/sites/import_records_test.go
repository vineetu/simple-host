package sites

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestImportRecordCapAndFeatures(t *testing.T) {
	m := NewImportManager(5)
	for i := 0; i < 21; i++ {
		id := fmt.Sprint(i)
		if err := m.Created(ImportLink{ID: id, Owner: "owner", Site: "blog"}); err != nil {
			t.Fatal(err)
		}
		m.Update(id, func(r *ImportRecord) { r.Finished = time.Now(); r.State = "done" })
	}
	if len(m.Records) != 20 || m.Records["0"] != nil {
		t.Fatal("oldest record not dropped")
	}
	m.Update("20", func(r *ImportRecord) {
		r.Report = &ImportReport{Features: []ImportFeature{{Kind: "search", Status: ImportStatusBuiltIn, Use: "Built-in search", Note: "Ready"}, {Kind: "hit_counter", Status: ImportStatusReplaced, Use: "site_analytics", Note: "Ask in chat"}, {Kind: "shop", Status: ImportStatusNotSupported, Use: "Static catalog", Note: "No checkout"}}}
	})
	out, err := m.Report("owner", "20")
	if err != nil {
		t.Fatal(err)
	}
	steps := out["next_steps"].([]string)
	if len(steps) != 4 || !strings.Contains(steps[0], "Built-in search") || !strings.Contains(steps[1], "site_analytics") || !strings.Contains(steps[2], "No checkout") || !strings.Contains(steps[3], "connect_domain") {
		t.Fatal(steps)
	}
}
