package papermetrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestKVMixedInstallMetrics(t *testing.T) {
	db := isolatedDB(t)
	ctx := context.Background()
	var owner, site, busy string
	if err := db.QueryRow(`INSERT INTO users(username) VALUES('mixed-metrics') RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'mixed') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'busy') RETURNING id`, owner).Scan(&busy); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{site, busy} {
		if _, err := db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind,kv_store) VALUES($1,'file','kv','sqlite')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'pg','kv')`, site); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'pg','long-key','{"text":"é"}')`, site); err != nil {
		t.Fatal(err)
	}
	var pgBytes, textBytes int64
	if err := db.QueryRow(`SELECT pg_column_size(value),octet_length(value::text) FROM site_storage_kv WHERE site_id=$1`, site).Scan(&pgBytes, &textBytes); err != nil {
		t.Fatal(err)
	}
	c := New(db, t.TempDir())
	calls := map[string]int{}
	// The injected boundary is tested with real KV files/admission by app/storage
	// and server wiring tests; the collector must sum once and skip a busy site.
	c.KVFileStats = func(_ context.Context, id string) (int64, int64, error) {
		calls[id]++
		if id == busy {
			return 0, 0, errors.New("storage busy")
		}
		return 2, 17, nil
	}
	now := time.Now().UTC()
	if err := c.dailySnapshot(ctx, now); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"kv_rows": 3, "kv_value_bytes": pgBytes, "kv_value_text_bytes": textBytes + 17, "kv_file_sites_skipped": 1} {
		var got int64
		if err := db.QueryRow(`SELECT value FROM paper_daily WHERE metric=$1`, name).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%d want %d: %v", name, got, want, err)
		}
	}
	if calls[site] != 1 || calls[busy] != 1 {
		t.Fatalf("callback counts: %v", calls)
	}
	if err := c.weeklyFootprint(ctx, now); err != nil {
		t.Fatal(err)
	}
	var n, rows, bytes int64
	if err := db.QueryRow(`SELECT count(*),sum(kv_rows),sum(kv_bytes) FROM paper_site_footprint`).Scan(&n, &rows, &bytes); err != nil || n != 1 || rows != 3 || bytes != pgBytes {
		t.Fatalf("footprint=%d/%d/%d: %v", n, rows, bytes, err)
	}
}
