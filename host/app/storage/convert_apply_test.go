package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/model"
	repo "github.com/vsriram/simple-host/host/repository/storage"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// The command's fixtures are the service fixtures of the equivalence suite,
// placed where the command looks for a site: <data dir>/by-id/<owner>/<site>.

type convertFixture struct {
	db      *sql.DB
	conv    *RulesConverter
	handle  string
	dataDir string
}

func convertHandle(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return "mr" + hex.EncodeToString(b[:])
}

// convertPlace gives the account a handle and links each twin's runtime
// directory into one data directory.
func convertPlace(t *testing.T, twins ...equivalenceTwin) convertFixture {
	t.Helper()
	f := convertFixture{db: admissionDB(t), handle: convertHandle(t), dataDir: t.TempDir()}
	seedInstallIdentity(t, f.db, f.dataDir)
	for _, twin := range twins {
		if _, err := f.db.Exec(`UPDATE users SET handle=$1 WHERE id=$2`, f.handle, twin.call.OwnerID); err != nil {
			t.Fatal(err)
		}
		site := filepath.Join(f.dataDir, "by-id", twin.call.OwnerID, twin.call.SiteName)
		if err := os.MkdirAll(site, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(twin.call.RuntimeDir, filepath.Join(site, "runtime")); err != nil {
			t.Fatal(err)
		}
	}
	f.conv = &RulesConverter{DB: f.db, DataDir: f.dataDir, Commit: "test-commit", SpamCheckAvailable: true}
	return f
}

func (f convertFixture) dryRun(t *testing.T) (ConvertPlan, string, error) {
	t.Helper()
	var out bytes.Buffer
	plan, err := f.conv.DryRun(context.Background(), []string{f.handle}, &out)
	return plan, out.String(), err
}

// only keeps one site's entries, as an operator may by editing a plan.
func (p ConvertPlan) only(siteID string) ConvertPlan {
	kept := []ConvertPlanEntry{}
	for _, e := range p.Resources {
		if e.SiteID == siteID {
			kept = append(kept, e)
		}
	}
	p.Resources = kept
	return p
}

func (p ConvertPlan) targets() []ConvertTarget {
	var out []ConvertTarget
	for _, e := range p.Resources {
		out = append(out, ConvertTarget{SiteID: e.SiteID, Resource: e.Name})
	}
	return out
}

// convertRows is every stored byte of a site's resources and table rows.
func convertRows(t *testing.T, db *sql.DB, siteID string) string {
	t.Helper()
	var out []string
	for _, query := range []string{
		`SELECT row_to_json(r)::text FROM site_storage_resources r WHERE site_id=$1 ORDER BY name`,
		`SELECT row_to_json(r)::text FROM site_storage_tables r WHERE site_id=$1 ORDER BY resource_name,table_name`,
	} {
		rows, err := db.Query(query, siteID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return strings.Join(out, "\n")
}

// convertStored is what the server reads of a site's resources: every column
// but the site, the record of the original configuration, and (rules being
// JSONB) with the sets in canonical form, for one resource.
func convertStored(t *testing.T, db *sql.DB, siteID, name string) string {
	t.Helper()
	var out []string
	for _, query := range []string{
		`SELECT row_to_json(r)::text FROM site_storage_resources r WHERE site_id=$1 AND name=$2`,
		`SELECT row_to_json(r)::text FROM site_storage_tables r WHERE site_id=$1 AND resource_name=$2 ORDER BY table_name`,
	} {
		rows, err := db.Query(query, siteID, name)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var row map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &row); err != nil {
				t.Fatal(err)
			}
			delete(row, "site_id")
			delete(row, "rules_converted_from")
			delete(row, "created_at")
			delete(row, "updated_at")
			if set := row["rules"]; set != nil && string(set) != "null" {
				decoded, errs := ruleengine.DecodeSet(set)
				if len(errs) != 0 {
					t.Fatal(errs)
				}
				row["rules"] = ruleengine.EncodeSet(decoded)
			}
			line, _ := json.Marshal(row)
			out = append(out, string(line))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return strings.Join(out, "\n")
}

// convertTree is every file under a directory with its mode, size and SHA-256.
func convertTree(t *testing.T, root string) string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s %s %d %s", strings.TrimPrefix(path, root), info.Mode(), info.Size(), info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000"))
		if d.Type().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			line += " " + hex.EncodeToString(sum[:])
		} else {
			// A directory's time moves with its entries, which are listed.
			line = fmt.Sprintf("%s %s", strings.TrimPrefix(path, root), info.Mode())
		}
		out = append(out, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func convertReload(t *testing.T, twin *equivalenceTwin) {
	t.Helper()
	var err error
	twin.call.Resource, err = twin.service.LoadResource(context.Background(), ResourceReadInput{SiteID: twin.call.SiteID, Name: twin.call.ResourceName})
	if err != nil {
		t.Fatal(err)
	}
}

func convertColumnsOf(t *testing.T, conn *sqlite3.Conn, table string) string {
	t.Helper()
	_, order, err := runtime.TableXInfo(conn, table)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(order, ",")
}

type countingTransport struct{ calls int }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls++
	return nil, errors.New("the dry run must not send a request")
}

// convertSite is a site with a SQLite database whose tables were made by hand
// (so they lack the server's columns), a key-value namespace and a file bucket.
func convertSite(t *testing.T, preset string) (equivalenceTwin, convertFixture) {
	t.Helper()
	twin := equivalenceFixture(t, "sqlite")
	equivalenceExec(t, twin.conn, `DROP TABLE entries; DROP TABLE overridden; CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT, extra TEXT); INSERT INTO notes(body) VALUES('first')`)
	m, _ := model.PresetMatrix(preset)
	equivalenceSave(t, &twin, equivalenceConfig{kind: "sqlite", matrix: m}, false)
	// A table made after the preset was saved. Keep the columns needed by
	// that preset; conversion may add its index without changing row answers.
	equivalenceExec(t, twin.conn, `CREATE TABLE later(id INTEGER PRIMARY KEY, body TEXT); INSERT INTO later(body) VALUES('second')`)
	if twin.call.Resource.OwnTables()("later") {
		equivalenceExec(t, twin.conn, `ALTER TABLE later ADD COLUMN visitor_id TEXT`)
	}
	if twin.call.Resource.Holds("later") {
		equivalenceExec(t, twin.conn, `ALTER TABLE later ADD COLUMN approved_at TEXT`)
	}
	if _, err := twin.service.PutStorageResource(context.Background(), ResourceInput{Call: twin.call, Name: "uploads", Kind: "files", AccessRequest: model.AccessRequest{Preset: "wall"}}); err != nil {
		t.Fatal(err)
	}
	return twin, convertPlace(t, twin)
}

func convertFilesRow(t *testing.T, db *sql.DB, siteID string) string {
	t.Helper()
	var row string
	if err := db.QueryRow(`SELECT row_to_json(r)::text FROM site_storage_resources r WHERE site_id=$1 AND name='uploads'`, siteID).Scan(&row); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestMigrateRulesDryRunChangesNothing(t *testing.T) {
	twin, f := convertSite(t, "wall")
	// A second database, in WAL mode and closed cleanly as the server leaves
	// one: an ordinary read-only open would create its -wal and -shm files.
	if _, err := twin.service.PutStorageResource(context.Background(), ResourceInput{Call: twin.call, Name: "closed", Kind: "sqlite", AccessRequest: model.AccessRequest{Preset: "public"}}); err != nil {
		t.Fatal(err)
	}
	closed, err := sqlite3.Open(filepath.Join(twin.call.RuntimeDir, "sqlite", "closed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE pages(id INTEGER PRIMARY KEY, body TEXT); INSERT INTO pages(body) VALUES('one')`); err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(twin.call.RuntimeDir, "sqlite", "closed.sqlite-wal")); !os.IsNotExist(err) {
		t.Fatalf("fixture: the closed database kept its log: %v", err)
	}
	files, rows := convertTree(t, twin.call.RuntimeDir), convertRows(t, f.db, twin.call.SiteID)
	data := convertTree(t, f.dataDir)
	transport := &countingTransport{}
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old }()
	plan, out, err := f.dryRun(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if transport.calls != 0 {
		t.Fatalf("%d HTTP calls", transport.calls)
	}
	if got := convertTree(t, twin.call.RuntimeDir); got != files {
		t.Fatalf("files changed:\n%s\nwas\n%s", got, files)
	}
	if got := convertTree(t, f.dataDir); got != data {
		t.Fatalf("data directory changed:\n%s\nwas\n%s", got, data)
	}
	if got := convertRows(t, f.db, twin.call.SiteID); got != rows {
		t.Fatalf("rows changed:\n%s\nwas\n%s", got, rows)
	}
	names := map[string]string{}
	for _, e := range plan.Resources {
		names[e.Name] = e.Kind
		if e.SHA256 != e.digest() || e.Fingerprint == "" || e.SchemaDigest == "" {
			t.Fatalf("entry %+v", e)
		}
	}
	if len(names) != 3 || names["rows"] != "sqlite" || names["closed"] != "sqlite" || names["data"] != "kv" {
		t.Fatalf("planned %v; a file bucket is never in scope", names)
	}
	// The plan file is for its owner only and reads back as written.
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := WriteConvertPlan(path, plan); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("plan mode %v %v", info.Mode(), err)
	}
	read, err := ReadConvertPlan(path)
	if err != nil || len(read.Resources) != 3 || !bytes.Equal(read.Resources[0].Rules, plan.Resources[0].Rules) || read.Resources[0].SHA256 != plan.Resources[0].SHA256 {
		t.Fatalf("read back: %v", err)
	}
	// One changed byte of an entry and the plan is refused.
	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, bytes.Replace(raw, []byte(`"write_mode": "add"`), []byte(`"write_mode": "full"`), 1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConvertPlan(path); !errors.Is(err, ErrConvertPlan) {
		t.Fatalf("edited plan: %v", err)
	}
	// A nonempty WAL cannot be inspected without risking writes to -shm.
	// Refusing it must leave every file (including both sidecars) and row intact.
	writer, err := sqlite3.Open(filepath.Join(twin.call.RuntimeDir, "sqlite", "closed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Exec(`PRAGMA wal_autocheckpoint=0; CREATE TABLE uncheckpointed(body TEXT)`); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(twin.call.RuntimeDir, "sqlite", "closed.sqlite-wal")); err != nil || fi.Size() == 0 {
		t.Fatalf("fixture: no nonempty WAL: %v", err)
	}
	files, rows, data = convertTree(t, twin.call.RuntimeDir), convertRows(t, f.db, twin.call.SiteID), convertTree(t, f.dataDir)
	plan, out, err = f.dryRun(t)
	if !errors.Is(err, runtime.ErrInspectWAL) {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, entry := range plan.Resources {
		if entry.Name == "closed" {
			t.Fatal("planned a database with a nonempty WAL")
		}
	}
	if transport.calls != 0 || convertTree(t, twin.call.RuntimeDir) != files || convertTree(t, f.dataDir) != data || convertRows(t, f.db, twin.call.SiteID) != rows {
		t.Fatal("the refused WAL inspection changed files or rows, or made an HTTP call")
	}
}

func TestMigrateRulesApplySkipsWhatChanged(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, twin *equivalenceTwin)
	}{
		{"owner save", func(t *testing.T, twin *equivalenceTwin) {
			m, _ := model.PresetMatrix("board")
			equivalenceSave(t, twin, equivalenceConfig{kind: "sqlite", matrix: m}, false)
		}},
		{"column renamed", func(t *testing.T, twin *equivalenceTwin) {
			equivalenceExec(t, twin.conn, `ALTER TABLE notes RENAME COLUMN extra TO renamed`)
		}},
		{"column dropped", func(t *testing.T, twin *equivalenceTwin) {
			equivalenceExec(t, twin.conn, `ALTER TABLE notes DROP COLUMN extra`)
		}},
		{"table added", func(t *testing.T, twin *equivalenceTwin) {
			equivalenceExec(t, twin.conn, `CREATE TABLE more(id INTEGER PRIMARY KEY)`)
		}},
		{"generated column added", func(t *testing.T, twin *equivalenceTwin) {
			equivalenceExec(t, twin.conn, `ALTER TABLE notes ADD COLUMN generated TEXT GENERATED ALWAYS AS (body) VIRTUAL`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			twin, f := convertSite(t, "wall")
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			tc.change(t, &twin)
			rows := convertRows(t, f.db, twin.call.SiteID)
			columns := convertColumnsOf(t, twin.conn, "later")
			var report bytes.Buffer
			err = f.conv.Apply(ctx, plan, &report)
			reason := "changed since the dry run"
			if tc.name == "generated column added" {
				reason = "needs a person: table notes: generated or hidden columns"
			} else if tc.name == "table added" {
				reason = "needs a person: table more: adding visitor_id"
			}
			if !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "skipped "+f.handle+" kv-test rows: "+reason) {
				t.Fatalf("%v\n%s", err, report.String())
			}
			// The key-value namespace of the same plan did not change and is converted.
			if !strings.Contains(report.String(), "converted "+f.handle+" kv-test data") {
				t.Fatal(report.String())
			}
			var recorded bool
			if err := f.db.QueryRow(`SELECT rules IS NOT NULL OR rules_converted_from IS NOT NULL FROM site_storage_resources WHERE site_id=$1 AND name='rows'`, twin.call.SiteID).Scan(&recorded); err != nil || recorded {
				t.Fatalf("something was recorded for a skipped resource: %v", err)
			}
			if convertColumnsOf(t, twin.conn, "later") != columns {
				t.Fatal("a skipped database was prepared")
			}
			if err := f.conv.Revert(ctx, []ConvertTarget{{SiteID: twin.call.SiteID, Resource: "data"}}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if got := convertRows(t, f.db, twin.call.SiteID); got != rows {
				t.Fatalf("rows:\n%s\nwas\n%s", got, rows)
			}
		})
	}
}

func TestMigrateRulesApplyRevalidatesBindingsWithoutTables(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(fmt.Sprintf("override=%v", override), func(t *testing.T) {
			twin := equivalenceFixture(t, "sqlite")
			equivalenceExec(t, twin.conn, `DROP TABLE entries; DROP TABLE overridden`)
			preset := "comments"
			tables := map[string]ResourceTableInput{}
			if override {
				preset = "public"
				tables["future"] = ResourceTableInput{AccessRequest: model.AccessRequest{Preset: "comments"}}
			}
			matrix, _ := model.PresetMatrix(preset)
			equivalenceSave(t, &twin, equivalenceConfig{kind: "sqlite", matrix: matrix, tables: tables}, false)
			f := convertPlace(t, twin)
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			// Turnstile was available for the dry run, but is no longer
			// configured when apply revalidates the converted sets.
			f.conv.SpamCheckAvailable = false
			rows := convertRows(t, f.db, twin.call.SiteID)
			var report bytes.Buffer
			err = f.conv.Apply(context.Background(), plan, &report)
			if !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "rows: needs a person: the converted rules do not compile here:") || !strings.Contains(report.String(), "auth.checked") {
				t.Fatalf("%v\n%s", err, report.String())
			}
			var recorded bool
			if err := f.db.QueryRow(`SELECT rules IS NOT NULL OR rules_converted_from IS NOT NULL FROM site_storage_resources WHERE site_id=$1 AND name='rows'`, twin.call.SiteID).Scan(&recorded); err != nil || recorded {
				t.Fatalf("something was recorded for a skipped resource: %v", err)
			}
			// The fixture's independent KV namespace may convert; restore
			// it before comparing every resource and table row.
			if err := f.conv.Revert(context.Background(), []ConvertTarget{{SiteID: twin.call.SiteID, Resource: "data"}}, &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if got := convertRows(t, f.db, twin.call.SiteID); got != rows {
				t.Fatalf("rows:\n%s\nwas\n%s", got, rows)
			}
		})
	}
}

func TestMigrateRulesNeedsAPersonForGeneratedColumns(t *testing.T) {
	for _, tc := range []struct {
		name, storage string
		ordinary      int
	}{
		{"virtual", "VIRTUAL", 2},
		{"stored", "STORED", 2},
		{"column limit", "VIRTUAL", 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			twin, f := convertSite(t, "public")
			columns := []string{"id INTEGER PRIMARY KEY", "body TEXT"}
			for len(columns) < tc.ordinary {
				columns = append(columns, fmt.Sprintf("extra%d TEXT", len(columns)))
			}
			columns = append(columns, "generated TEXT GENERATED ALWAYS AS (body) "+tc.storage)
			equivalenceExec(t, twin.conn, "CREATE TABLE generated("+strings.Join(columns, ",")+"); INSERT INTO generated(body) VALUES('one')")
			files, rows := convertTree(t, twin.call.RuntimeDir), convertRows(t, f.db, twin.call.SiteID)
			plan, out, err := f.dryRun(t)
			if !errors.Is(err, ErrConvertAttention) || len(plan.NeedsPerson) != 1 || plan.NeedsPerson[0].Resource != "rows" || !strings.Contains(plan.NeedsPerson[0].Reason, "table generated: generated or hidden columns") {
				t.Fatalf("%v\n%s", err, out)
			}
			for _, entry := range plan.Resources {
				if entry.Name == "rows" {
					t.Fatal("planned a database with generated columns")
				}
			}
			if convertTree(t, twin.call.RuntimeDir) != files || convertRows(t, f.db, twin.call.SiteID) != rows {
				t.Fatal("the dry run changed files or rows")
			}
		})
	}
}

// Reproduce a plan from the old converter, which did not model the handler's
// pre-preset owner check. Keep all fingerprint, schema and hash checks intact
// so apply must reject it on the independently repeated answer comparison.
func convertUncheckedEntry(t *testing.T, f convertFixture, siteID, name string) ConvertPlanEntry {
	t.Helper()
	r, err := repo.ConvertTarget(context.Background(), f.db, siteID, name)
	if err != nil {
		t.Fatal(err)
	}
	m := *f.conv
	m.db = f.db
	e, err := m.examine(context.Background(), r, false, false)
	if err != nil || len(e.needs) != 0 {
		t.Fatalf("unchecked examination: %v needs=%v", err, e.needs)
	}
	return e.entry()
}

func TestMigrateRulesPrePresetOwnerOnSiteDifferences(t *testing.T) {
	for _, kind := range []string{"sqlite", "kv"} {
		t.Run(kind, func(t *testing.T) {
			twin := equivalenceFixture(t, kind)
			matrix, _ := model.PresetMatrix("inbox")
			equivalenceSave(t, &twin, equivalenceConfig{kind: kind, matrix: matrix}, false)
			f := convertPlace(t, twin)
			if _, err := f.db.Exec(`UPDATE site_storage_resources SET read_policy='owner',write_policy='anyone',write_mode='add',acc_read=NULL,acc_add=NULL,acc_edit=NULL,acc_delete=NULL,preset_base='' WHERE site_id=$1 AND name=$2`, twin.call.SiteID, twin.call.ResourceName); err != nil {
				t.Fatal(err)
			}
			convertReload(t, &twin)
			if twin.call.Resource.Access != nil {
				t.Fatal("fixture must have no resource preset")
			}
			files, rows := convertTree(t, twin.call.RuntimeDir), convertRows(t, f.db, twin.call.SiteID)
			plan, out, err := f.dryRun(t)
			if !errors.Is(err, ErrConvertAttention) {
				t.Fatalf("%v\n%s", err, out)
			}
			for _, entry := range plan.Resources {
				if entry.Name == twin.call.ResourceName {
					t.Fatal("planned a resource that grants new owner-on-site rights")
				}
			}
			for _, action := range []string{"read", "edit", "delete"} {
				old := "refused:add_only"
				if action == "read" {
					old = "refused:forbidden"
				}
				found := false
				for _, d := range plan.Differences {
					if d.Resource == twin.call.ResourceName && d.Caller == "owner-on-site" && d.Action == action && d.Old == old && d.New == "allowed" {
						found = true
						if !strings.Contains(out, d.Line()+"\n") {
							t.Fatalf("missing printed difference: %+v", d)
						}
					}
				}
				if !found {
					t.Fatalf("missing owner-on-site %s difference\n%s", action, out)
				}
			}
			if convertTree(t, twin.call.RuntimeDir) != files || convertRows(t, f.db, twin.call.SiteID) != rows {
				t.Fatal("dry run changed files or rows")
			}
			plan.Resources = []ConvertPlanEntry{convertUncheckedEntry(t, f, twin.call.SiteID, twin.call.ResourceName)}
			var report bytes.Buffer
			if err := f.conv.Apply(context.Background(), plan, &report); !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "conversion changes answers:") || !strings.Contains(report.String(), "converted=0 already_converted=0 skipped=1 failed=0") {
				t.Fatalf("%v\n%s", err, report.String())
			}
			var recorded bool
			if err := f.db.QueryRow(`SELECT rules IS NOT NULL OR rules_converted_from IS NOT NULL FROM site_storage_resources WHERE site_id=$1 AND name=$2`, twin.call.SiteID, twin.call.ResourceName).Scan(&recorded); err != nil || recorded {
				t.Fatalf("skipped resource recorded: %v, %v", recorded, err)
			}
			if convertTree(t, twin.call.RuntimeDir) != files || convertRows(t, f.db, twin.call.SiteID) != rows {
				t.Fatal("apply recorded, prepared or converted a differing resource")
			}
			// Keep recovery coverage for NULL acc_* columns: reproduce the
			// state an older converter could have written, then revert it.
			entry := plan.Resources[0]
			sha, err := convertSetsSHA(entry.Rules, entry.Tables)
			if err != nil {
				t.Fatal(err)
			}
			record := convertRecord{Version: convertPlanVersion, Original: entry.Original, RulesSHA256: sha}
			ctx := context.Background()
			if ok, err := repo.RecordConvertedFrom(ctx, f.db, entry.SiteID, entry.Name, convertEncode(record)); err != nil || !ok {
				t.Fatalf("seed old conversion record: %v %v", ok, err)
			}
			if err := repo.ConvertToRules(ctx, f.db, entry.SiteID, entry.Name, entry.Rules, nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := f.conv.Revert(ctx, plan.targets(), &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			if convertTree(t, twin.call.RuntimeDir) != files || convertRows(t, f.db, twin.call.SiteID) != rows {
				t.Fatal("revert did not restore the legacy configuration exactly")
			}
		})
	}
}

func TestMigrateRulesNeedsAPersonForRequiredColumnAdditions(t *testing.T) {
	for _, width := range []int{3, 99, 100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			twin, f := convertSite(t, "public")
			// Reproduce a pre-preset, add-only database with a real table.
			// Unlike a preset save, its original setup never added visitor_id.
			if _, err := f.db.Exec(`UPDATE site_storage_resources SET read_policy='anyone',write_policy='anyone',write_mode='add',acc_read=NULL,acc_add=NULL,acc_edit=NULL,acc_delete=NULL,preset_base='' WHERE site_id=$1 AND name='rows'`, twin.call.SiteID); err != nil {
				t.Fatal(err)
			}
			convertReload(t, &twin)
			equivalenceExec(t, twin.conn, `DROP TABLE notes; DROP TABLE later`)
			// A plan from before the table existed also exercises apply's own
			// safety check, independently of the dry run's exclusion.
			plan, out, err := f.dryRun(t)
			if !errors.Is(err, ErrConvertAttention) || len(plan.Differences) == 0 {
				t.Fatalf("%v\n%s", err, out)
			}
			plan.Resources = []ConvertPlanEntry{convertUncheckedEntry(t, f, twin.call.SiteID, "rows")}
			columns := []string{"id INTEGER PRIMARY KEY", "body TEXT"}
			for len(columns) < width {
				columns = append(columns, fmt.Sprintf("extra%d TEXT", len(columns)))
			}
			equivalenceExec(t, twin.conn, "CREATE TABLE notes("+strings.Join(columns, ",")+"); INSERT INTO notes(body) VALUES('one')")
			callers := []Call{{}, {VisitorID: "author"}, {VisitorID: twin.call.OwnerID}, {Owner: true}}
			answers := func() []any {
				var answers []any
				for _, c := range callers {
					for _, op := range []equivalenceOperation{{name: "list", action: "read"}, {name: "get", action: "read", id: 1}} {
						body, refusal := equivalenceRun(t, twin, c, "sqlite", "notes", op)
						if refusal.Code != "" || len(body.(map[string]any)["columns"].([]any)) != width {
							t.Fatalf("legacy %s: body=%v refusal=%+v", op.name, body, refusal)
						}
						answers = append(answers, body)
					}
				}
				return answers
			}
			before := convertEncode(answers())
			convertAssertSchemaNeedsPerson(t, twin, f, plan, "table notes: adding visitor_id")
			if !bytes.Equal(before, convertEncode(answers())) {
				t.Fatal("skipped conversion changed legacy row responses")
			}
			// The original add still succeeds at the 100-column boundary and
			// stores only the supplied data, without introducing authorship.
			_, refusal := equivalenceRun(t, twin, Call{VisitorID: "author"}, "sqlite", "notes", equivalenceOperation{name: "add", action: "add"})
			if refusal.Code != "" || strings.Contains(convertColumnsOf(t, twin.conn, "notes"), "visitor_id") {
				t.Fatalf("legacy add: %+v", refusal)
			}
			stmt, _, err := twin.conn.Prepare(`SELECT body FROM notes WHERE id=2`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if !stmt.Step() || stmt.ColumnText(0) != "changed" || stmt.Step() {
				t.Fatal("legacy add did not store its row")
			}
		})
	}
	t.Run("approval column", func(t *testing.T) {
		twin, f := convertSite(t, "comments")
		m, _ := model.PresetMatrix("comments")
		equivalenceSave(t, &twin, equivalenceConfig{kind: "sqlite", matrix: m, hold: true}, false)
		if !twin.call.Resource.ApproveFirst || !strings.Contains(convertColumnsOf(t, twin.conn, "notes"), "approved_at") {
			t.Fatal("fixture must hold posts with an existing approval column")
		}
		plan, out, err := f.dryRun(t)
		if err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		equivalenceExec(t, twin.conn, `ALTER TABLE notes DROP COLUMN approved_at`)
		convertAssertSchemaNeedsPerson(t, twin, f, plan, "table notes: adding approved_at")
	})
}

func TestMigrateRulesNeedsAPersonForSystemColumnCasing(t *testing.T) {
	for _, column := range []string{"VISITOR_ID", "Written_By", "Created_At", "UPDATED_AT", "Approved_At"} {
		t.Run(column, func(t *testing.T) {
			twin, f := convertSite(t, "public")
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			equivalenceExec(t, twin.conn, "ALTER TABLE notes ADD COLUMN "+column+" TEXT")
			equivalenceExec(t, twin.conn, "UPDATE notes SET "+column+"='other-visitor'")
			convertAssertSchemaNeedsPerson(t, twin, f, plan, "noncanonical system column "+column)

			if column == "VISITOR_ID" {
				// The preset exposes this spelling; the converted rules redact it.
				call := twin.call
				call.Owner, call.VisitorID = false, "reader"
				before, err := twin.service.TableGet(context.Background(), TableInput{Call: call, Table: "notes", ID: 1})
				if err != nil {
					t.Fatal(err)
				}
				m, _ := model.PresetMatrix("public")
				equivalenceSave(t, &twin, equivalenceConfig{kind: "sqlite", matrix: m}, true)
				call.Resource = twin.call.Resource
				after, err := twin.service.TableGet(context.Background(), TableInput{Call: call, Table: "notes", ID: 1})
				if err != nil {
					t.Fatal(err)
				}
				if before.Body["row"].([]any)[3] != "other-visitor" || after.Body["row"].([]any)[3] != nil {
					t.Fatalf("expected author redaction difference: preset=%v rules=%v", before.Body, after.Body)
				}
			}
		})
	}
}

// Check both the dry run and apply's independent revalidation, before it
// records provenance or prepares SQLite. The supplied plan predates the DDL.
func convertAssertSchemaNeedsPerson(t *testing.T, twin equivalenceTwin, f convertFixture, plan ConvertPlan, reason string) {
	t.Helper()
	files, rows := convertTree(t, twin.call.RuntimeDir), convertRows(t, f.db, twin.call.SiteID)
	current, out, err := f.dryRun(t)
	if !errors.Is(err, ErrConvertAttention) || len(current.NeedsPerson) != 1 || !strings.Contains(current.NeedsPerson[0].Reason, reason) {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, entry := range current.Resources {
		if entry.Name == "rows" {
			t.Fatal("planned a schema that changes behavior")
		}
	}
	if convertTree(t, twin.call.RuntimeDir) != files || convertRows(t, f.db, twin.call.SiteID) != rows {
		t.Fatal("dry run changed files or rows")
	}
	for _, entry := range plan.Resources {
		if entry.Name == "rows" {
			plan.Resources = []ConvertPlanEntry{entry}
			break
		}
	}
	var report bytes.Buffer
	if err := f.conv.Apply(context.Background(), plan, &report); !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "needs a person:") || !strings.Contains(report.String(), reason) {
		t.Fatalf("%v\n%s", err, report.String())
	}
	if convertRows(t, f.db, twin.call.SiteID) != rows || convertTree(t, twin.call.RuntimeDir) != files {
		t.Fatal("apply recorded, prepared or converted an excluded resource")
	}
}

func TestMigrateRulesNeedsAPersonForInvalidStoredState(t *testing.T) {
	for _, kind := range []string{"sqlite", "kv"} {
		t.Run(kind, func(t *testing.T) {
			twin := equivalenceFixture(t, kind)
			matrix, _ := model.PresetMatrix("private")
			equivalenceSave(t, &twin, equivalenceConfig{kind: kind, matrix: matrix}, false)
			f := convertPlace(t, twin)
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if _, err := f.db.Exec(`UPDATE site_storage_resources SET preset_base='rules' WHERE site_id=$1 AND name=$2`, twin.call.SiteID, twin.call.ResourceName); err != nil {
				t.Fatal(err)
			}
			convertReload(t, &twin)
			if twin.call.Resource.Rules != nil || twin.call.Resource.RulesStatus() != "invalid" {
				t.Fatal("fixture must have missing rules and an invalid stored state")
			}
			// Model a plan made by the old converter for this exact invalid
			// state. Apply must reject it even when its fingerprint matches.
			for _, entry := range plan.Resources {
				if entry.Name == twin.call.ResourceName {
					entry.Fingerprint = twin.call.Resource.Fingerprint()
					entry.Original, err = repo.LoadConvertOriginal(context.Background(), f.db, twin.call.SiteID, entry.Name)
					if err != nil {
						t.Fatal(err)
					}
					entry.SHA256 = entry.digest()
					plan.Resources = []ConvertPlanEntry{entry}
					break
				}
			}
			assertRefusal := func() {
				_, refusal := equivalenceRun(t, twin, Call{VisitorID: "author"}, kind, "entries", equivalenceOperation{name: "list", action: "read"})
				if refusal.Code != "rules_invalid" {
					t.Fatalf("invalid resource refusal: %+v", refusal)
				}
			}
			assertRefusal()
			files, rows := convertTree(t, twin.call.RuntimeDir), convertRows(t, f.db, twin.call.SiteID)
			current, out, err := f.dryRun(t)
			if !errors.Is(err, ErrConvertAttention) || len(current.NeedsPerson) != 1 || !strings.Contains(current.NeedsPerson[0].Reason, "stored rules state is invalid") {
				t.Fatalf("%v\n%s", err, out)
			}
			for _, entry := range current.Resources {
				if entry.Name == twin.call.ResourceName {
					t.Fatal("planned an invalid resource")
				}
			}
			var report bytes.Buffer
			if err := f.conv.Apply(context.Background(), plan, &report); !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "needs a person: the stored rules state is invalid") {
				t.Fatalf("%v\n%s", err, report.String())
			}
			if convertRows(t, f.db, twin.call.SiteID) != rows || convertTree(t, twin.call.RuntimeDir) != files {
				t.Fatal("conversion changed an invalid resource")
			}
			convertReload(t, &twin)
			assertRefusal()
		})
	}
}

func TestMigrateRulesNeedsAPersonForForeignKeyOwnReads(t *testing.T) {
	for _, parentPreset := range []string{"personal", "public"} {
		t.Run(parentPreset, func(t *testing.T) {
			twin, f := convertSite(t, "public")
			equivalenceExec(t, twin.conn, `CREATE TABLE parents(id INTEGER PRIMARY KEY, body TEXT, visitor_id TEXT COLLATE BINARY);
				INSERT INTO parents VALUES(1, 'parent', CAST('author' AS BLOB));
				CREATE TABLE children(id INTEGER PRIMARY KEY, body TEXT, parent_id INTEGER DEFAULT 1)`)
			matrix, _ := model.PresetMatrix("public")
			matrix.Add = model.WhoSignedIn
			equivalenceSave(t, &twin, equivalenceConfig{kind: "sqlite", matrix: matrix, tables: map[string]ResourceTableInput{
				"parents": {AccessRequest: model.AccessRequest{Preset: parentPreset}},
			}}, false)
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			// Only the foreign key changes; table_info and its digest do not.
			// Apply must inspect references again even with a matching digest.
			equivalenceExec(t, twin.conn, `DROP TABLE children;
				CREATE TABLE children(id INTEGER PRIMARY KEY, body TEXT, parent_id INTEGER DEFAULT 1 REFERENCES parents(id), visitor_id TEXT)`)
			assertAdd := func() {
				_, refusal := equivalenceRun(t, twin, Call{VisitorID: "author"}, "sqlite", "children", equivalenceOperation{name: "add", action: "add"})
				if refusal.Code != "" {
					t.Fatalf("child insert: %+v", refusal)
				}
			}
			if parentPreset == "personal" {
				convertAssertSchemaNeedsPerson(t, twin, f, plan, "foreign-key parent parents uses own reads")
				convertReload(t, &twin)
				if twin.call.Resource.RulesStatus() != "preset" || twin.call.Resource.ConvertedFrom != nil {
					t.Fatal("converted a resource with potentially unequal reference checks")
				}
			} else {
				current, out, err := f.dryRun(t)
				if err != nil || len(current.NeedsPerson) != 0 {
					t.Fatalf("%v\n%s", err, out)
				}
				if err := f.conv.Apply(context.Background(), plan, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				convertReload(t, &twin)
				if twin.call.Resource.RulesStatus() != "rules" {
					t.Fatal("public parent should remain convertible")
				}
			}
			assertAdd()
		})
	}
}

func TestMigrateRulesNeedsAPersonForForeignKeyAuthenticatedReads(t *testing.T) {
	for _, tc := range []struct {
		read         string
		parentPreset string
		ownerOnSite  bool
	}{
		{read: "signed-in", parentPreset: "board"},
		{read: "owner", parentPreset: "private", ownerOnSite: true},
	} {
		t.Run(tc.read, func(t *testing.T) {
			twin, f := convertSite(t, "public")
			equivalenceExec(t, twin.conn, `CREATE TABLE parents(id INTEGER PRIMARY KEY, body TEXT);
				INSERT INTO parents VALUES(1, 'parent');
				CREATE TABLE children(id INTEGER PRIMARY KEY, body TEXT, parent_id INTEGER DEFAULT 1, visitor_id TEXT)`)
			matrix, _ := model.PresetMatrix("public")
			cfg := equivalenceConfig{kind: "sqlite", matrix: matrix, tables: map[string]ResourceTableInput{
				"parents":  {AccessRequest: model.AccessRequest{Preset: tc.parentPreset}},
				"children": {AccessRequest: model.AccessRequest{Preset: "inbox"}},
			}}
			equivalenceSave(t, &twin, cfg, false)
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			// Keep table_info unchanged, so apply must re-examine the foreign key
			// even though the saved configuration and schema digest still match.
			equivalenceExec(t, twin.conn, `DROP TABLE children;
				CREATE TABLE children(id INTEGER PRIMARY KEY, body TEXT, parent_id INTEGER DEFAULT 1 REFERENCES parents(id), visitor_id TEXT)`)
			var visitor string
			if err := f.db.QueryRow(`INSERT INTO users(username) VALUES(gen_random_uuid()::text) RETURNING id`).Scan(&visitor); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { f.db.Exec(`DELETE FROM users WHERE id=$1`, visitor) })
			// The session has already been resolved when the account is suspended.
			caller := Call{VisitorID: visitor, VisitorSessionUserID: visitor, OwnerOnSite: tc.ownerOnSite}
			if _, err := f.db.Exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, visitor); err != nil {
				t.Fatal(err)
			}
			assertAdd := func(want string) {
				t.Helper()
				// A Hack owner-on-site is a real user, distinct from the site's
				// holding account. Keep that resolved identity on the request.
				call := twin.call
				call.Owner, call.OwnerOnSite = false, caller.OwnerOnSite
				call.VisitorID, call.VisitorSessionUserID = caller.VisitorID, caller.VisitorSessionUserID
				call.CSRFOK = true
				in := TableInput{Call: call, Table: "children", Values: map[string]json.RawMessage{"body": json.RawMessage(`"changed"`)}}
				in.Authorized = twin.service.AuthorizeTableWrite(context.Background(), in, "add")
				out, err := twin.service.TableAdd(context.Background(), in)
				refusal := equivalenceRefused(t, out, err)
				if refusal.Code != want {
					t.Fatalf("suspended child insert: %+v, want %q", refusal, want)
				}
			}
			convertAssertSchemaNeedsPerson(t, twin, f, plan, "foreign-key parent parents uses "+tc.read+" reads")
			convertReload(t, &twin)
			if twin.call.Resource.RulesStatus() != "preset" || twin.call.Resource.ConvertedFrom != nil {
				t.Fatal("apply recorded or converted an excluded resource")
			}
			assertAdd("")
			// Pin the behavior difference that makes automatic conversion unsafe.
			equivalenceSave(t, &twin, cfg, true)
			assertAdd("invalid_reference")
		})
	}
}

func TestMigrateRulesAuthorCollation(t *testing.T) {
	for _, tc := range []struct{ collation, author string }{{"BINARY", "author"}, {"NOCASE", "Author"}, {"RTRIM", "author "}} {
		t.Run(tc.collation, func(t *testing.T) {
			twin, f := convertSite(t, "personal")
			create := func(collation string) {
				equivalenceExec(t, twin.conn, "CREATE TABLE collated(id INTEGER PRIMARY KEY, body TEXT, visitor_id TEXT COLLATE "+collation+")")
			}
			create("BINARY")
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			// Only the collation changes: table_info and its digest stay equal.
			equivalenceExec(t, twin.conn, "DROP TABLE collated")
			create(tc.collation)
			stmt, _, err := twin.conn.Prepare("INSERT INTO collated(body, visitor_id) VALUES('one', ?)")
			if err != nil {
				t.Fatal(err)
			}
			if err := stmt.BindText(1, tc.author); err != nil {
				t.Fatal(err)
			}
			if err := stmt.Exec(); err != nil {
				t.Fatal(err)
			}
			stmt.Close()
			if tc.collation == "BINARY" {
				if err := f.conv.Apply(context.Background(), plan, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				return
			}
			convertAssertSchemaNeedsPerson(t, twin, f, plan, "visitor_id collation "+tc.collation)
			call := twin.call
			call.Owner, call.VisitorID = false, "author"
			if _, err := twin.service.TableGet(context.Background(), TableInput{Call: call, Table: "collated", ID: 1}); err != nil {
				t.Fatalf("preset should expose collated match: %v", err)
			}
			m, _ := model.PresetMatrix("personal")
			equivalenceSave(t, &twin, equivalenceConfig{kind: "sqlite", matrix: m}, true)
			call.Resource = twin.call.Resource
			_, err = twin.service.TableGet(context.Background(), TableInput{Call: call, Table: "collated", ID: 1})
			var refused *Error
			if !errors.As(err, &refused) || refused.Code != "row_not_found" {
				t.Fatalf("rules should hide nonbinary match: %v", err)
			}
		})
	}
}

// Exit without running Go or SQLite cleanup, immediately after preparation's
// COMMIT and before its checkpoint. The parent supplies its isolated database.
func TestMigrateRulesWALCrashChild(t *testing.T) {
	dir := os.Getenv("MIGRATE_RULES_CRASH_DIR")
	if dir == "" {
		return
	}
	plan, err := ReadConvertPlan(filepath.Join(dir, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := RulesConverter{DB: admissionDB(t), DataDir: dir, SpamCheckAvailable: true}
	ctx := runtime.WithSQLiteStageHook(context.Background(), func(stage string) {
		if stage == "after_commit" {
			os.Exit(86)
		}
	})
	if err := m.Apply(ctx, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash boundary was not reached")
}

func TestMigrateRulesResumesAfterWALCrash(t *testing.T) {
	for _, shm := range []string{"intact", "missing", "truncated"} {
		t.Run(shm, func(t *testing.T) {
			twin, f := convertSite(t, "wall")
			equivalenceExec(t, twin.conn, "PRAGMA journal_mode=WAL")
			if err := twin.conn.Close(); err != nil {
				t.Fatal(err)
			}
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			plan.Resources = []ConvertPlanEntry{plan.Resources[1]}
			if plan.Resources[0].Name != "rows" {
				t.Fatal(plan.Resources[0].Name)
			}
			if err := WriteConvertPlan(filepath.Join(f.dataDir, "plan.json"), plan); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestMigrateRulesWALCrashChild$")
			cmd.Env = append(os.Environ(), "TESTDB_ISOLATE=0", "MIGRATE_RULES_CRASH_DIR="+f.dataDir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 86 {
				t.Fatalf("crash child: %v %s", err, output)
			}
			waitInstallLockFree(t, f.db)
			path := filepath.Join(twin.call.RuntimeDir, "sqlite", "rows.sqlite")
			if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
				t.Fatalf("crash did not leave a nonempty WAL: %v", err)
			}
			if shm == "missing" {
				if err := os.Remove(path + "-shm"); err != nil {
					t.Fatal(err)
				}
			} else if shm == "truncated" {
				if err := os.Truncate(path+"-shm", 1); err != nil {
					t.Fatal(err)
				}
			}
			convertReload(t, &twin)
			if twin.call.Resource.Rules != nil || twin.call.Resource.ConvertedFrom == nil {
				t.Fatal("expected recorded provenance and unconverted rules after crash")
			}
			files, rows := convertTree(t, twin.call.RuntimeDir), convertRows(t, f.db, twin.call.SiteID)
			if _, _, err := f.dryRun(t); !errors.Is(err, runtime.ErrInspectWAL) {
				t.Fatalf("dry run should refuse the WAL: %v", err)
			}
			if convertTree(t, twin.call.RuntimeDir) != files || convertRows(t, f.db, twin.call.SiteID) != rows {
				t.Fatal("dry run changed crash files or rows")
			}
			var report bytes.Buffer
			if err := f.conv.Apply(context.Background(), plan, &report); err != nil {
				t.Fatalf("resume: %v\n%s", err, report.String())
			}
			convertReload(t, &twin)
			if twin.call.Resource.RulesStatus() != "rules" {
				t.Fatal("resume did not finish conversion")
			}
		})
	}
}

// A run stopped after each step leaves the state the slice's table names, and
// running apply again finishes the conversion.
func TestMigrateRulesStopAfterEachStepAndRerun(t *testing.T) {
	ctx := context.Background()
	for _, stage := range []string{"recorded", "prepared", "before-commit", "converted"} {
		t.Run(stage, func(t *testing.T) {
			twin, f := convertSite(t, "wall")
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			plan.Resources = []ConvertPlanEntry{plan.Resources[1]}
			if plan.Resources[0].Name != "rows" {
				t.Fatal(plan.Resources[0].Name)
			}
			before := twin.call.Resource
			stop := errors.New("stopped at " + stage)
			f.conv.boundary = func(at string) error {
				if at == stage {
					return stop
				}
				return nil
			}
			if err := f.conv.Apply(ctx, plan, &bytes.Buffer{}); !errors.Is(err, stop) {
				t.Fatalf("stop: %v", err)
			}
			convertReload(t, &twin)
			now := twin.call.Resource
			stmt, _, err := twin.conn.Prepare(`SELECT 1 FROM sqlite_schema WHERE type='index' AND name='sh_visitor_later'`)
			if err != nil {
				t.Fatal(err)
			}
			indexed := stmt.Step()
			if err := stmt.Err(); err != nil {
				t.Fatal(err)
			}
			stmt.Close()
			switch stage {
			case "recorded":
				// Unchanged for everyone; only the record is set.
				if now.Fingerprint() != before.Fingerprint() || now.ConvertedFrom == nil || indexed {
					t.Fatalf("after step 1: %+v indexed=%v", now, indexed)
				}
			case "prepared", "before-commit":
				// As above, plus an index that leaves row answers unchanged.
				if now.Fingerprint() != before.Fingerprint() || now.ConvertedFrom == nil || !indexed {
					t.Fatalf("after step 2: %+v indexed=%v", now, indexed)
				}
			default:
				if now.RulesStatus() != "rules" || !indexed {
					t.Fatalf("after step 3: %s", now.RulesStatus())
				}
			}
			f.conv.boundary = nil
			var report bytes.Buffer
			if err := f.conv.Apply(ctx, plan, &report); err != nil {
				t.Fatalf("rerun: %v\n%s", err, report.String())
			}
			want := "converted " + f.handle + " kv-test rows"
			if stage == "converted" {
				want = "already " + want
			}
			if !strings.Contains(report.String(), want+"\n") {
				t.Fatal(report.String())
			}
			convertReload(t, &twin)
			if twin.call.Resource.RulesStatus() != "rules" {
				t.Fatal(twin.call.Resource.RulesStatus())
			}
			// The rows the tables held are still there, unapproved nothing, authors empty.
			stmt, _, err = twin.conn.Prepare(`SELECT body, visitor_id FROM later`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if !stmt.Step() || stmt.ColumnText(0) != "second" || stmt.ColumnType(1) != sqlite3.NULL || stmt.Step() {
				t.Fatal("the additive step changed a row")
			}
		})
	}
}

func TestMigrateRulesApplyTwiceRevertAndFileBucket(t *testing.T) {
	ctx := context.Background()
	twin, f := convertSite(t, "wall")
	bucket := convertFilesRow(t, f.db, twin.call.SiteID)
	original := convertRows(t, f.db, twin.call.SiteID)
	plan, out, err := f.dryRun(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := f.conv.Apply(ctx, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	once := convertRows(t, f.db, twin.call.SiteID)
	tree := convertTree(t, twin.call.RuntimeDir)
	var report bytes.Buffer
	if err := f.conv.Apply(ctx, plan, &report); err != nil || !strings.Contains(report.String(), "converted=0 already_converted=2 skipped=0 failed=0") {
		t.Fatalf("second apply: %v\n%s", err, report.String())
	}
	if convertRows(t, f.db, twin.call.SiteID) != once || convertTree(t, twin.call.RuntimeDir) != tree {
		t.Fatal("the second apply changed something")
	}
	// The record holds the original configuration, the commit and the sets' SHA-256.
	convertReload(t, &twin)
	record, ok := decodeConvertRecord(twin.call.Resource.ConvertedFrom)
	want, _ := resourceSetsSHA(twin.call.Resource)
	if !ok || record.Commit != "test-commit" || record.RulesSHA256 != want || record.Original.ReadPolicy != "anyone" || record.Original.AccDelete == nil || *record.Original.AccDelete != model.WhoOwn {
		t.Fatalf("record %+v", record)
	}
	if convertFilesRow(t, f.db, twin.call.SiteID) != bucket {
		t.Fatal("apply touched a file bucket")
	}
	// A file bucket cannot be reverted either: it was never converted.
	report.Reset()
	if err := f.conv.Revert(ctx, []ConvertTarget{{SiteID: twin.call.SiteID, Resource: "uploads"}}, &report); !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "not converted by migrate-rules") {
		t.Fatalf("%v\n%s", err, report.String())
	}
	if err := f.conv.Revert(ctx, plan.targets(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := convertRows(t, f.db, twin.call.SiteID); got != original {
		t.Fatalf("revert left\n%s\nwas\n%s", got, original)
	}
	// The column apply added stays.
	if !strings.HasSuffix(convertColumnsOf(t, twin.conn, "later"), ",visitor_id") {
		t.Fatal("revert removed a column")
	}
	report.Reset()
	if err := f.conv.Revert(ctx, plan.targets(), &report); !errors.Is(err, ErrConvertIncomplete) || strings.Count(report.String(), "not converted by migrate-rules") != 2 {
		t.Fatalf("second revert: %v\n%s", err, report.String())
	}
	if convertFilesRow(t, f.db, twin.call.SiteID) != bucket {
		t.Fatal("revert touched a file bucket")
	}
}

func TestMigrateRulesRevertAfterOwnerEditIsSkipped(t *testing.T) {
	ctx := context.Background()
	twin, f := convertSite(t, "wall")
	plan, out, err := f.dryRun(t)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if err := f.conv.Apply(ctx, plan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	edited := json.RawMessage(`{"policies":[{"name":"read","command":"read","using":"auth.signed_in"}]}`)
	tables := map[string]ResourceTableInput{}
	if _, err := twin.service.PutStorageResource(ctx, ResourceInput{Call: twin.call, Name: "rows", Kind: "sqlite", Rules: &edited, Tables: &tables}); err != nil {
		t.Fatal(err)
	}
	rows := convertRows(t, f.db, twin.call.SiteID)
	var report bytes.Buffer
	err = f.conv.Revert(ctx, plan.targets(), &report)
	if !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "rows: rules were changed after conversion") || !strings.Contains(report.String(), "reverted=1 skipped=1") {
		t.Fatalf("%v\n%s", err, report.String())
	}
	var edit, data bool
	if err := f.db.QueryRow(`SELECT (SELECT rules IS NOT NULL FROM site_storage_resources WHERE site_id=$1 AND name='rows'), (SELECT rules IS NULL FROM site_storage_resources WHERE site_id=$1 AND name='data')`, twin.call.SiteID).Scan(&edit, &data); err != nil || !edit || !data {
		t.Fatalf("%v rows kept=%v data reverted=%v", err, edit, data)
	}
	if !strings.Contains(convertRows(t, f.db, twin.call.SiteID), strings.SplitN(rows, "\n", 3)[1]) {
		t.Fatal("the edited resource changed")
	}
	// An owner who went back to a preset is not put back either.
	m, _ := model.PresetMatrix("public")
	equivalenceSave(t, &twin, equivalenceConfig{kind: "sqlite", matrix: m}, false)
	report.Reset()
	if err := f.conv.Revert(ctx, []ConvertTarget{{SiteID: twin.call.SiteID, Resource: "rows"}}, &report); !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "rules were changed after conversion") {
		t.Fatalf("%v\n%s", err, report.String())
	}
}

func TestMigrateRulesNeedsAPersonAndDifferences(t *testing.T) {
	ctx := context.Background()
	twin, f := convertSite(t, "wall")
	site := twin.call.SiteID
	for _, row := range []string{
		// Pages could write this database with SQL.
		`('legacy_sql','sqlite','anyone','signed-in','full',NULL,NULL,NULL,NULL,'')`,
		// Pages still read this one with SQL.
		`('legacy_read','sqlite','anyone','owner','full',NULL,NULL,NULL,NULL,'')`,
		// Full-mode key-value from before presets: its writers would be cleared.
		`('legacy_kv','kv','signed-in','signed-in','full',NULL,NULL,NULL,NULL,'')`,
		// Anyone may change it: no rule may say so.
		`('legacy_open','kv','anyone','anyone','full',NULL,NULL,NULL,NULL,'')`,
		// Add-only from before presets: conversion would grant owner-on-site rights.
		`('legacy_inbox','sqlite','owner','anyone','add',NULL,NULL,NULL,NULL,'')`,
		// A matrix no save accepts (change wider than read): rules decide otherwise.
		`('odd','sqlite','owner','signed-in','full','owner','signed-in','signed-in','signed-in','')`,
	} {
		if _, err := f.db.Exec(`INSERT INTO site_storage_resources(name,kind,read_policy,write_policy,write_mode,acc_read,acc_add,acc_edit,acc_delete,preset_base,site_id) SELECT v.*, $1::uuid FROM (VALUES `+row+`) v`, site); err != nil {
			t.Fatal(err)
		}
	}
	rows := convertRows(t, f.db, site)
	plan, out, err := f.dryRun(t)
	if !errors.Is(err, ErrConvertAttention) {
		t.Fatalf("%v\n%s", err, out)
	}
	planned := map[string]bool{}
	for _, e := range plan.Resources {
		planned[e.Name] = true
	}
	if len(planned) != 2 || !planned["rows"] || !planned["data"] || planned["legacy_inbox"] {
		t.Fatalf("planned %v\n%s", planned, out)
	}
	needs := map[string]string{}
	for _, n := range plan.NeedsPerson {
		needs[n.Resource] += n.Reason
	}
	for name, want := range map[string]string{"legacy_sql": "clear its visitor_id values", "legacy_read": "still read this database with SQL", "legacy_kv": "clear the writers", "legacy_open": "needs a manual decision"} {
		if !strings.Contains(needs[name], want) {
			t.Fatalf("%s: %q", name, needs[name])
		}
	}
	if len(needs) != 4 || len(plan.Differences) == 0 || len(plan.Notes) != 0 {
		t.Fatalf("needs=%v differences=%d notes=%v", needs, len(plan.Differences), plan.Notes)
	}
	differing := map[string]bool{}
	for _, d := range plan.Differences {
		if (d.Resource != "odd" && d.Resource != "legacy_inbox") || d.Account != f.handle || d.Site != "kv-test" || d.Line() != strings.Join([]string{d.Account, d.Site, d.Resource, d.Table, d.Caller, d.Action, d.Old, d.New}, " ") || !strings.Contains(out, d.Line()+"\n") {
			t.Fatalf("difference %+v", d)
		}
		differing[d.Resource] = true
	}
	if !differing["odd"] || !differing["legacy_inbox"] {
		t.Fatalf("missing differing resource: %v", differing)
	}
	if convertRows(t, f.db, site) != rows {
		t.Fatal("the dry run changed a row")
	}
	var report bytes.Buffer
	err = f.conv.Apply(ctx, plan, &report)
	if err != nil || !strings.Contains(report.String(), "converted=2 already_converted=0 skipped=0 failed=0") {
		t.Fatalf("%v\n%s", err, report.String())
	}
	var untouched int
	if err := f.db.QueryRow(`SELECT count(*) FROM site_storage_resources WHERE site_id=$1 AND name IN ('legacy_sql','legacy_read','legacy_kv','legacy_open','legacy_inbox','odd','uploads') AND rules IS NULL AND rules_converted_from IS NULL`, site).Scan(&untouched); err != nil || untouched != 7 {
		t.Fatalf("untouched=%d %v", untouched, err)
	}
	// Revert restores converted resources; rejected legacy settings stay intact.
	if err := f.conv.Revert(ctx, []ConvertTarget{{site, "data"}, {site, "rows"}}, &report); err != nil {
		t.Fatalf("%v\n%s", err, report.String())
	}
	if got := convertRows(t, f.db, site); got != rows {
		t.Fatalf("revert left\n%s\nwas\n%s", got, rows)
	}
	// A plan entry made needs-a-person after the dry run is skipped at apply.
	if _, err := f.db.Exec(`UPDATE site_storage_resources SET acc_read=NULL,acc_add=NULL,acc_edit=NULL,acc_delete=NULL,preset_base='',read_policy='anyone',write_policy='signed-in',write_mode='full' WHERE site_id=$1 AND name='rows'`, site); err != nil {
		t.Fatal(err)
	}
	report.Reset()
	if err := f.conv.Apply(ctx, plan, &report); !errors.Is(err, ErrConvertIncomplete) || !strings.Contains(report.String(), "kv-test rows: changed since the dry run") {
		t.Fatalf("%v\n%s", err, report.String())
	}
	// An unknown handle is an error, not an empty plan.
	if _, err := f.conv.DryRun(ctx, []string{"no-such-" + f.handle}, &bytes.Buffer{}); err == nil || errors.Is(err, ErrConvertAttention) {
		t.Fatalf("unknown handle: %v", err)
	}
}

func TestMigrateRulesRefusesWhileAServerRuns(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"apply", "revert"} {
		t.Run(mode, func(t *testing.T) {
			twin, f := convertSite(t, "wall")
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if mode == "revert" {
				if err := f.conv.Apply(ctx, plan, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
			}
			server, err := runtime.LockMaintenance(f.dataDir, false)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			hold, err := HoldInstallShared(ctx, os.Getenv("DB_DSN"))
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Close()
			for _, directory := range []string{"same", "different"} {
				t.Run(directory, func(t *testing.T) {
					converter := *f.conv
					want := runtime.ErrDataDirInUse
					if directory == "different" {
						converter.DataDir = t.TempDir()
						seedInstallIdentity(t, f.db, converter.DataDir)
						want = repo.ErrDatabaseInUse
						// Include an existing lock file in the unchanged-files check.
						lock, err := runtime.LockMaintenance(converter.DataDir, false)
						if err != nil {
							t.Fatal(err)
						}
						lock.Close()
					}
					files := convertTree(t, converter.DataDir) + convertTree(t, twin.call.RuntimeDir)
					rows := convertRows(t, f.db, twin.call.SiteID)
					var report bytes.Buffer
					if mode == "apply" {
						err = converter.Apply(ctx, plan, &report)
					} else {
						err = converter.Revert(ctx, plan.targets(), &report)
					}
					if !errors.Is(err, want) || report.Len() != 0 {
						t.Fatalf("beside a server: %v %s", err, &report)
					}
					if convertRows(t, f.db, twin.call.SiteID) != rows || convertTree(t, converter.DataDir)+convertTree(t, twin.call.RuntimeDir) != files {
						t.Fatal("a refused run changed files or rows")
					}
				})
			}
			// The dry run is the one mode that may run beside the server.
			if _, _, err := f.dryRun(t); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrateRulesRefusesWrongOwner(t *testing.T) {
	for _, mode := range []string{"apply", "revert"} {
		t.Run(mode, func(t *testing.T) {
			twin, f := convertSite(t, "wall")
			plan, out, err := f.dryRun(t)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if mode == "revert" {
				if err := f.conv.Apply(context.Background(), plan, &bytes.Buffer{}); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(f.dataDir, ".maintenance.lock")); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate another effective user.
			f.conv.euid = func() int { return os.Geteuid() + 1 }
			files := convertTree(t, f.dataDir) + convertTree(t, twin.call.RuntimeDir)
			rows := convertRows(t, f.db, twin.call.SiteID)
			var report bytes.Buffer
			if mode == "apply" {
				err = f.conv.Apply(context.Background(), plan, &report)
			} else {
				err = f.conv.Revert(context.Background(), plan.targets(), &report)
			}
			if !errors.Is(err, runtime.ErrDataDirOwner) || !strings.Contains(err.Error(), f.dataDir) || report.Len() != 0 {
				t.Fatalf("wrong owner: %v %s", err, &report)
			}
			if convertRows(t, f.db, twin.call.SiteID) != rows || convertTree(t, f.dataDir)+convertTree(t, twin.call.RuntimeDir) != files {
				t.Fatal("wrong-owner refusal changed files or rows")
			}
		})
	}
}

func TestMigrateRulesRefusesWrongInstall(t *testing.T) {
	for _, mode := range []string{"apply", "revert"} {
		for _, identity := range []string{"missing", "different"} {
			t.Run(mode+"/"+identity, func(t *testing.T) {
				twin, f := convertSite(t, "wall")
				plan, out, err := f.dryRun(t)
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				if mode == "revert" {
					if err := f.conv.Apply(context.Background(), plan, &bytes.Buffer{}); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(filepath.Join(f.dataDir, ".maintenance.lock")); err != nil {
						t.Fatal(err)
					}
				}
				path := runtime.InstallIDPath(f.dataDir)
				if identity == "missing" {
					err = os.Remove(path)
				} else {
					err = os.WriteFile(path, []byte("another-install\n"), 0644)
				}
				if err != nil {
					t.Fatal(err)
				}
				files := convertTree(t, f.dataDir) + convertTree(t, twin.call.RuntimeDir)
				rows := convertRows(t, f.db, twin.call.SiteID)
				var report bytes.Buffer
				if mode == "apply" {
					err = f.conv.Apply(context.Background(), plan, &report)
				} else {
					err = f.conv.Revert(context.Background(), plan.targets(), &report)
				}
				if !errors.Is(err, ErrInstallIdentity) || report.Len() != 0 {
					t.Fatalf("wrong install: %v %s", err, &report)
				}
				if convertRows(t, f.db, twin.call.SiteID) != rows || convertTree(t, f.dataDir)+convertTree(t, twin.call.RuntimeDir) != files {
					t.Fatal("wrong-install refusal changed files or rows")
				}
			})
		}
	}
}

func TestMigrateRulesInstallLock(t *testing.T) {
	for _, stage := range []string{"recorded", "before-commit", "before-revert-commit"} {
		for _, loseConnection := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/connection-loss=%v", stage, loseConnection), func(t *testing.T) {
				twin, f := convertSite(t, "wall")
				other := otherPool(t)
				plan, out, err := f.dryRun(t)
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				// One target makes the interrupted transaction's state unambiguous.
				plan.Resources = plan.Resources[:1]
				run := func() error { return f.conv.Apply(context.Background(), plan, &bytes.Buffer{}) }
				if stage == "before-revert-commit" {
					if err := run(); err != nil {
						t.Fatal(err)
					}
					run = func() error { return f.conv.Revert(context.Background(), plan.targets(), &bytes.Buffer{}) }
				}
				stop := errors.New("injected failure")
				var rows string
				f.conv.boundary = func(at string) error {
					if at != stage {
						return nil
					}
					holders := installLockHolders(t, other)
					if len(holders) != 1 {
						t.Fatalf("install lock holders: %v", holders)
					}
					if lock, err := runtime.LockMaintenance(f.dataDir, false); !errors.Is(err, runtime.ErrMaintenanceRunning) {
						if lock != nil {
							lock.Close()
						}
						t.Fatalf("server took directory lock: %v", err)
					}
					if hold, err := HoldInstallShared(context.Background(), os.Getenv("DB_DSN")); !errors.Is(err, repo.ErrDatabaseMaintenance) {
						if hold != nil {
							hold.Close()
						}
						t.Fatalf("server took database lock: %v", err)
					}
					rows = convertRows(t, other, twin.call.SiteID)
					if loseConnection {
						if _, err := other.Exec(`SELECT pg_terminate_backend($1)`, holders[0]); err != nil {
							t.Fatal(err)
						}
						waitInstallLockFree(t, other)
						return nil
					}
					return stop
				}
				if err := run(); err == nil || !loseConnection && !errors.Is(err, stop) {
					t.Fatalf("interruption lost: %v", err)
				}
				if rows == "" || convertRows(t, other, twin.call.SiteID) != rows {
					t.Fatal("conversion committed after interruption")
				}
				if got := installLockHolders(t, other); len(got) != 0 {
					t.Fatalf("install lock still held after error: %v", got)
				}
				f.conv.boundary = nil
				if err := run(); err != nil {
					t.Fatalf("rerun: %v", err)
				}
				if got := installLockHolders(t, other); len(got) != 0 {
					t.Fatalf("install lock still held after success: %v", got)
				}
			})
		}
	}
}
