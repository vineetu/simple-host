package papermetrics

import (
	"context"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// The three counters follow the model's RulesStatus, resource by resource,
// and leave file buckets out.
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
	const fill = `'owner','owner','add','nobody','nobody','nobody','nobody','rules',FALSE`
	for _, row := range []string{
		// On presets, and saved before presets.
		`('preset','sqlite','anyone','owner','add','anyone','owner','owner','owner','public',FALSE,NULL)`,
		`('older','kv','owner','owner','full',NULL,NULL,NULL,NULL,'',FALSE,NULL)`,
		// On rules, whole: one without and one with table sets.
		`('rules','kv',` + fill + `,'` + set + `')`,
		`('tables','sqlite',` + fill + `,'` + set + `')`,
		// Broken shapes: the fill without rules; rules under a preset an older
		// program saved; a table row without rules; a held flag.
		`('fill-only','sqlite',` + fill + `,NULL)`,
		`('overwritten','sqlite','anyone','owner','add','anyone','owner','owner','owner','public',FALSE,'` + set + `')`,
		`('bad-table','sqlite',` + fill + `,'` + set + `')`,
		`('held','sqlite','owner','owner','add','nobody','nobody','nobody','nobody','rules',TRUE,'` + set + `')`,
		// A file bucket is in none of the three.
		`('bucket','files','anyone','signed-in','add','anyone','signed-in','owner','own','wall',FALSE,NULL)`,
	} {
		if _, err := db.Exec(`INSERT INTO site_storage_resources(name,kind,read_policy,write_policy,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,approve_first,rules,site_id) SELECT v.c1,v.c2,v.c3,v.c4,v.c5,v.c6,v.c7,v.c8,v.c9,v.c10,v.c11,v.c12::jsonb,$1::uuid FROM (VALUES `+row+`) v(c1,c2,c3,c4,c5,c6,c7,c8,c9,c10,c11,c12)`, site); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []string{
		`('tables','a','nobody','nobody','nobody','nobody','rules','` + set + `')`,
		`('bad-table','a','nobody','nobody','nobody','nobody','rules',NULL)`,
	} {
		if _, err := db.Exec(`INSERT INTO site_storage_tables(resource_name,table_name,acc_read,acc_add,acc_edit,acc_delete,preset_base,rules,site_id) SELECT v.c1,v.c2,v.c3,v.c4,v.c5,v.c6,v.c7,v.c8::jsonb,$1::uuid FROM (VALUES `+row+`) v(c1,c2,c3,c4,c5,c6,c7,c8)`, site); err != nil {
			t.Fatal(err)
		}
	}
	// What the model says of each stored resource.
	want := map[string]int64{}
	rows, err := db.Query(`SELECT r.name,r.kind,r.read_policy,r.write_policy,r.write_mode,r.acc_read,r.acc_add,r.acc_edit,r.acc_delete,r.preset_base,r.approve_first,r.rules::text FROM site_storage_resources r WHERE r.site_id=$1 AND r.kind <> 'files'`, site)
	if err != nil {
		t.Fatal(err)
	}
	var resources []model.Resource
	for rows.Next() {
		var x model.Resource
		var read, add, edit, del, set *string
		if err := rows.Scan(&x.Name, &x.Kind, &x.Read, &x.Write, &x.WriteMode, &read, &add, &edit, &del, &x.PresetBase, &x.ApproveFirst, &set); err != nil {
			t.Fatal(err)
		}
		if read != nil {
			x.Access = &model.AccessMatrix{Read: *read, Add: *add, Edit: *edit, Delete: *del}
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
		tables, err := db.Query(`SELECT table_name,acc_read,acc_add,acc_edit,acc_delete,preset_base,rules::text FROM site_storage_tables WHERE site_id=$1 AND resource_name=$2`, site, x.Name)
		if err != nil {
			t.Fatal(err)
		}
		for tables.Next() {
			var name string
			var rule model.TableRule
			var set *string
			if err := tables.Scan(&name, &rule.Matrix.Read, &rule.Matrix.Add, &rule.Matrix.Edit, &rule.Matrix.Delete, &rule.PresetBase, &set); err != nil {
				t.Fatal(err)
			}
			if set != nil {
				rule.Rules = []byte(*set)
			}
			x.Tables[name] = rule
		}
		tables.Close()
		want[map[string]string{"rules": "resources_rules", "invalid": "resources_rules_invalid", "preset": "resources_preset"}[x.RulesStatus()]]++
	}
	if want["resources_rules"] != 2 || want["resources_rules_invalid"] != 4 || want["resources_preset"] != 2 {
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
