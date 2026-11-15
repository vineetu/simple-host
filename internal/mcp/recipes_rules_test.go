package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/storage"
)

// Captured from the untouched recipes at 54cc3504, before R3-S6b.
func TestRulesRecipesFlagOffBytes(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	defer NewServer(Config{})
	NewServer(Config{})
	const frozen = `{
  "hack-event": "85e149a6ea85622fc9098a768c7c38509a7383fb6d7386c221327247ec96bd46",
  "inbox": "5e2c32ce6400878501167a57aa62cfc8919d30c0924365b5ae724d1c116f6c2e",
  "people": "fa23c69c9b5adc3338e4e7b50c20e174d8f2f62da16e350c3befc215749efa6a",
  "wall": "0b04b64653e7eb664c12d5cea1dbcb6eec24ae78640d99c949f75e101dfc5923",
  "board": "2b9f601d91fff1696d5b87fc9ece239bfa3598465d603bd6bc9a3c6ed5158ae2",
  "private": "0a68af4b26b33e93582a1c60f1311047c39c84f249ca8d4f5c100ebdf818df7c",
  "public": "8442b3220e017ac844f979f8e32aca1393d327a9d012c30fd492a4635a6d9498",
  "admin": "eb3619456a7fc3e0e9a02a20d45302a4fba1060552d990d7f2b4530b66985144",
  "records": "7725237c085105ff77aae4b24509b71aa04daf397315416b52548f62d2556c7b",
  "gallery": "5e83eaf5afe0d332bbb334a3daefa4d36bc71e2903ce2d7760ceb8749369a862",
  "portal": "dfcf9251cd73da3ab26537fc20c4b8c4adec3b8f745d83b60dbeb44df39b797c",
  "comments": "45480a914a5348ae2d1dc6a5d3052182867eee9a7ba8bf659869282e1d62aeac",
  "personal": "5b6b7c76e6e0d345f39e590f91cbbab9e19a41afbf733f930bc2ec88c58313e7",
  "wordpress": "926e080dec7524a594b29c0f276f5e7b4f97a0ea990924747e13fae700829a36",
  "booking": "f0f9d2b6b0443732244febdda99125594df0ad49cdccf4697cae354f1bb39f63"
}`
	var hashes map[string]string
	if err := json.Unmarshal([]byte(frozen), &hashes); err != nil {
		t.Fatal(err)
	}
	for topic, want := range hashes {
		raw, err := readRecipe(topic)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want {
			t.Errorf("%s legacy bytes changed", topic)
		}
	}
	for _, topic := range recipeTopics {
		canonical := topic
		if alias, ok := recipeAliases[topic]; ok {
			canonical = alias
		}
		raw, _ := recipeFiles.ReadFile("recipes/" + canonical + ".md")
		text, err := PageRecipe(topic, "shop")
		if err != nil {
			t.Fatal(err)
		}
		if text != addressText(strings.ReplaceAll(string(raw), "{site}", "shop")) {
			t.Errorf("%s flag-off response changed", topic)
		}
	}
	// Both public dispatch paths re-read the flag, including a cached tool.
	for _, tool := range []Tool{pageRecipeTool(), hackPageRecipeTool()} {
		c := &call{server: NewServer(Config{})}
		args := map[string]any{"topic": "booking", "site": "shop"}
		before, err := tool.run(c, args)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("STORAGE_RULES", "on")
		on, err := tool.run(c, args)
		if err != nil {
			t.Fatal(err)
		}
		if on.Text == before.Text || !strings.Contains(on.Text, `"policies"`) {
			t.Error("recipe did not switch at request time")
		}
		if on.Structured["recipe"] != on.Text {
			t.Error("structured recipe differs from text")
		}
		t.Setenv("STORAGE_RULES", "off")
		after, err := tool.run(c, args)
		if err != nil || after.Text != before.Text {
			t.Errorf("legacy response not restored: %v", err)
		}
	}
	if _, err := PageRecipe("rules/booking", "shop"); err == nil {
		t.Error("flag-on recipe accessible as a flag-off topic")
	}
}

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
	defer NewServer(Config{})
	NewServer(Config{})
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
			// Presets describe files or fields to remove from an old import report,
			// never an access choice for data rows.
			for _, line := range strings.Split(text, "\n") {
				if topic == "wordpress" && strings.Contains(line, "remove preset and approve_first; send rules instead") {
					continue
				}
				if strings.Contains(strings.ToLower(line), "preset") && !strings.Contains(strings.ToLower(line), "file") && topic != "gallery" {
					t.Errorf("data access still names a preset: %s", line)
				}
			}
		})
	}
}

func TestRulesWordPressImportSetupAdvice(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	defer NewServer(Config{})
	NewServer(Config{})
	text, err := PageRecipe("wordpress", "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, advice := range []string{
		"keep the report's site and resource names",
		"run its storage_sql_schema calls unchanged",
		"remove preset and approve_first; send rules instead",
		"Use the corresponding policies in the mapping below",
		"when approve_first is true or the feature is marked moderated, include the restrictive approval policy",
		"guestbooks always need it unless the person asks otherwise",
		"Keep the spam check on comments and guestbooks",
	} {
		if !strings.Contains(text, advice) {
			t.Errorf("WordPress recipe lacks %q", advice)
		}
	}
	if strings.Contains(text, "run the setup calls the report gives, exactly") {
		t.Error("WordPress recipe still directs agents to replay legacy access fields")
	}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		if strings.Count(strings.ReplaceAll(line, `\|`, ""), "|") != 4 {
			t.Errorf("WordPress mapping must have three cells: %s", line)
		}
		if strings.HasPrefix(line, "| Comments on posts and pages |") &&
			!strings.Contains(line, `auth.role == "owner" \|\| approved_at is not null | name "comments" |`) {
			t.Error("comments mapping lost the approval expression or setup cell")
		}
	}
}

func TestRulesHackRecipes(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	defer NewServer(Config{})
	NewServer(Config{})
	for _, topic := range append(append([]string{}, recipeTopics...), "event") {
		text, err := HackPageRecipe(topic, "my-event")
		switch topic {
		case "portal", "comments", "guestbook", "people", "wordpress":
			if err == nil {
				t.Errorf("%s product refusal lost", topic)
			}
		default:
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "Ask who should read") {
				t.Errorf("%s missing question", topic)
			}
			if topic != "event" && topic != "gallery" && !strings.Contains(text, `"policies"`) {
				t.Errorf("%s has no rules", topic)
			}
			if strings.Contains(text, "https://simple-host.app/auth.js") || strings.Contains(text, "Simple Host account") {
				t.Errorf("%s has Host wording", topic)
			}
		}
	}
}

func TestRulesRecipeAdaptations(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	defer NewServer(Config{})
	NewServer(Config{})
	admin, err := PageRecipe("admin", "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, advice := range []string{"Preserve the existing resource rules and every table override", "an inbox must keep anonymous submissions and owner-only reads", "NEW orders resource only"} {
		if !strings.Contains(admin, advice) {
			t.Errorf("admin recipe lacks %q", advice)
		}
	}
	for _, topic := range []string{"public", "menu"} {
		text, err := HackPageRecipe(topic, "my-event")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "files resource with preset public") || strings.Contains(text, "same preset") {
			t.Errorf("%s has ambiguous file access advice", topic)
		}
	}
	t.Setenv("STORAGE_RULES", "off")
	text, err := HackPageRecipe("public", "my-event")
	if err != nil || !strings.Contains(text, "files resource with the same preset") {
		t.Fatalf("flag-off Hack photo advice changed: %v", err)
	}
}
