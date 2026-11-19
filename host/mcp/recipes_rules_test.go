package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appstorage "github.com/vsriram/simple-host/host/app/storage"
	"github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

type recipeSetup struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
	Body struct {
		Kind   string          `json:"kind"`
		Preset string          `json:"preset"`
		Rules  json.RawMessage `json:"rules"`
	} `json:"body"`
}

func recipeCalls(t *testing.T, text, tool string) []recipeSetup {
	t.Helper()
	var out []recipeSetup
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, tool+" ") {
			continue
		}
		var setup recipeSetup
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, tool+" ")), &setup); err != nil {
			t.Fatal(err)
		}
		out = append(out, setup)
	}
	return out
}

func TestRulesRecipeExamples(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	defer NewServer(testConfig(Config{}))
	NewServer(testConfig(Config{}))
	for _, topic := range recipeTopics {
		t.Run(topic, func(t *testing.T) {
			text, err := PageRecipe(topic, "shop")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "Ask who should read") {
				t.Error("missing access question")
			}
			schemas := recipeCalls(t, text, "storage_sql_schema")
			// The admin page deliberately reuses the records recipe's existing tables.
			if topic == "admin" {
				records, _ := PageRecipe("records", "shop")
				schemas = recipeCalls(t, records, "storage_sql_schema")
			}
			setups := recipeCalls(t, text, "storage_set_resource")
			if len(setups) == 0 {
				t.Fatal("recipe has no worked setup example")
			}
			for _, setup := range setups {
				if setup.Body.Kind == "files" {
					if setup.Body.Preset == "" || len(setup.Body.Rules) > 0 {
						t.Error("files must keep presets")
					}
					continue
				}
				if setup.Body.Preset != "" {
					t.Error("SQLite/KV access taught as a preset")
				}
				set, errs := ruleengine.DecodeSet(setup.Body.Rules)
				if len(errs) > 0 {
					t.Fatal(errs)
				}
				bindings := appstorage.PlatformSchema(true)
				if errs := ruleengine.ValidateSyntax(set, bindings, ruleengine.DefaultLimits()); len(errs) > 0 {
					t.Fatal(errs)
				}
				if setup.Body.Kind == "kv" {
					table := ruleengine.Table{Name: setup.Name, RowKey: `"key"`, Columns: []ruleengine.Column{{Name: "key"}, {Name: "value"}, {Name: "visitor_id"}}}
					if _, errs := ruleengine.Compile(set, table, bindings, ruleengine.DefaultLimits()); len(errs) > 0 {
						t.Fatal(errs)
					}
					continue
				}
				conn, err := sqlite3.Open(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				tables := []string{}
				for _, schema := range schemas {
					if schema.Name != setup.Name {
						continue
					}
					if err := conn.Exec(schema.SQL); err != nil {
						t.Fatalf("schema example: %v", err)
					}
					if strings.HasPrefix(schema.SQL, "CREATE TABLE IF NOT EXISTS ") {
						tables = append(tables, strings.Fields(schema.SQL)[5])
					}
				}
				for _, name := range tables {
					// Production prepares these server-owned columns when policies reference
					// them or permit ordinary visitors to add. They are never page inputs.
					cols, err := storage.TableColumns(conn, name)
					if err != nil {
						t.Fatal(err)
					}
					for _, col := range []string{"visitor_id", "approved_at"} {
						if _, ok := cols[col]; !ok {
							if err := conn.Exec("ALTER TABLE " + storage.Quote(name) + " ADD COLUMN " + col + " TEXT"); err != nil {
								t.Fatal(err)
							}
						}
					}
					cols, err = storage.TableColumns(conn, name)
					if err != nil {
						t.Fatal(err)
					}
					table := ruleengine.Table{Name: name, RowKey: "rowid"}
					for col := range cols {
						table.Columns = append(table.Columns, ruleengine.Column{Name: col})
					}
					if _, errs := ruleengine.Compile(set, table, bindings, ruleengine.DefaultLimits()); len(errs) > 0 {
						t.Fatalf("%s: %v", name, errs)
					}
				}
			}
			// Presets describe files,
			// never an access choice for data rows.
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(strings.ToLower(line), "preset") && !strings.Contains(strings.ToLower(line), "file") && topic != "gallery" {
					t.Errorf("data access still names a preset: %s", line)
				}
			}
		})
	}
}

func TestRulesRecipeAdaptations(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	defer NewServer(testConfig(Config{}))
	NewServer(testConfig(Config{}))
	admin, err := PageRecipe("admin", "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, advice := range []string{"Preserve the existing resource rules and every table override", "an inbox must keep anonymous submissions and owner-only reads", "NEW orders resource only"} {
		if !strings.Contains(admin, advice) {
			t.Errorf("admin recipe lacks %q", advice)
		}
	}
}
