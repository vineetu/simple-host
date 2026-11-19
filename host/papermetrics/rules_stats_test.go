package papermetrics

import (
	"context"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// The two counters follow the model's RulesStatus, resource by resource. A
// resource with no stored rules is owner only, which counts as rules.
func TestRulesStatusMetrics(t *testing.T) {
	db := isolatedDB(t)
	var owner, site string
	if err := db.QueryRow(`INSERT INTO users(username) VALUES('rules-metrics') RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'rules-metrics') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	const set = `{"version":1,"policies":[]}`
	for _, row := range []string{
		`('older','kv',NULL)`,
		`('rules','kv','` + set + `')`,
		`('tables','sqlite','` + set + `')`,
		`('bad-table','sqlite','` + set + `')`,
		`('bucket','files','` + set + `')`,
	} {
		if _, err := db.Exec(`INSERT INTO site_storage_resources(name,kind,rules,site_id) SELECT v.c1,v.c2,v.c3::jsonb,$1::uuid FROM (VALUES `+row+`) v(c1,c2,c3)`, site); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []string{`('tables','a','` + set + `')`, `('bad-table','a',NULL)`} {
		if _, err := db.Exec(`INSERT INTO site_storage_tables(resource_name,table_name,rules,site_id) SELECT v.c1,v.c2,v.c3::jsonb,$1::uuid FROM (VALUES `+row+`) v(c1,c2,c3)`, site); err != nil {
			t.Fatal(err)
		}
	}
	// What the model says of each stored resource.
	want := map[string]int64{}
	rows, err := db.Query(`SELECT r.name,r.rules::text FROM site_storage_resources r WHERE r.site_id=$1`, site)
	if err != nil {
		t.Fatal(err)
	}
	var resources []model.Resource
	for rows.Next() {
		var x model.Resource
		var set *string
		if err := rows.Scan(&x.Name, &set); err != nil {
			t.Fatal(err)
		}
		if set != nil {
			x.Rules = []byte(*set)
		}
		resources = append(resources, x)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, x := range resources {
		x.Tables = map[string]model.TableRule{}
		tables, err := db.Query(`SELECT table_name,rules::text FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, site, x.Name)
		if err != nil {
			t.Fatal(err)
		}
		for tables.Next() {
			var name string
			var set *string
			if err := tables.Scan(&name, &set); err != nil {
				t.Fatal(err)
			}
			rule := model.TableRule{}
			if set != nil {
				rule.Rules = []byte(*set)
			}
			if x.Rules != nil {
				x.Tables[name] = rule
			}
		}
		tables.Close()
		want[map[string]string{"rules": "resources_rules", "invalid": "resources_rules_invalid"}[x.RulesStatus()]]++
	}
	if len(want) != 2 || want["resources_rules"] != 4 || want["resources_rules_invalid"] != 1 {
		t.Fatalf("fixture: the model counts %v", want)
	}
	if err := New(db, t.TempDir()).dailySnapshot(context.Background(), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	for name, n := range want {
		var got int64
		if err := db.QueryRow(`SELECT value FROM paper_daily WHERE metric=$1`, name).Scan(&got); err != nil || got != n {
			t.Fatalf("%s=%d want %d: %v", name, got, n, err)
		}
	}
}
