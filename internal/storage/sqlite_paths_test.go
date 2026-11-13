package storage

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// Production opens are deliberately restricted to the four admission-owned
// mechanics. Resolve the import alias, so renaming sqlite3 cannot bypass this.
func TestSQLiteOpenAllowList(t *testing.T) {
	root := filepath.Join("..", "..")
	allowed := map[string]bool{
		"internal/storage/runtime_sqlite.go":      true,
		"internal/storage/runtime_sql_prepare.go": true,
		"internal/storage/runtime_matrix.go":      true,
		"internal/storage/export_sqlite.go":       true,
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		aliases := map[string]bool{}
		for _, imp := range f.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			if name != "github.com/ncruces/go-sqlite3" {
				continue
			}
			alias := "sqlite3"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			aliases[alias] = true
		}
		rel, _ := filepath.Rel(root, path)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			var name string
			switch f := call.Fun.(type) {
			case *ast.SelectorExpr:
				if id, ok := f.X.(*ast.Ident); ok && aliases[id.Name] {
					name = f.Sel.Name
				}
			case *ast.Ident:
				if aliases["."] {
					name = f.Name
				}
			}
			if (name == "Open" || name == "OpenContext") && !allowed[filepath.ToSlash(rel)] {
				t.Errorf("SQLite open outside allow-list: %s", rel)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteOpenPathsPanicCleanup(t *testing.T) {
	for _, kind := range []string{"table", "legacy", "matrix", "export"} {
		stages := []string{"after_open", "close"}
		if kind == "table" || kind == "matrix" {
			stages = append(stages, "after_prepare", "after_step")
		}
		if kind == "matrix" {
			stages = append(stages, "before_commit", "after_commit")
		}
		if kind == "export" {
			stages = append(stages, "before_checkpoint")
		}
		for _, stage := range stages {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "data.sqlite")
				seed, err := sqlite3.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = seed.Exec("CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
					t.Fatal(err)
				}
				seed.Close()
				var opened *sqlite3.Conn
				closes := 0
				ctx := context.WithValue(context.Background(), recoveryConnHookKey{}, func(c *sqlite3.Conn) error {
					opened = c
					return c.Trace(sqlite3.TRACE_CLOSE, func(sqlite3.TraceEvent, any, any) error { closes++; return nil })
				})
				injected, committed, changed := false, false, false
				ctx = context.WithValue(ctx, sqliteStageKey{}, func(at string) {
					if at == stage {
						injected = true
						panic("injected")
					}
				})
				var got error
				func() {
					defer func() {
						if p := recover(); p != nil {
							if p != "injected" {
								t.Fatalf("unexpected panic: %v", p)
							}
							got = errors.New("panic")
						}
					}()
					switch kind {
					case "table":
						_, done, err := OpenTableConn(ctx, path, true, time.Second, 256<<20, func() (int64, error) { return 1 << 20, nil })
						got = err
						if done != nil {
							closeTableConn(done)
						}
					case "legacy":
						c, err := OpenLegacySQLite(ctx, path, 256<<20, 1<<20, true)
						got = err
						if c != nil {
							closeTableConn(func() { defer c.Close(); sqliteStage(ctx, "close") })
						}
					case "matrix":
						fi, err := os.Stat(path)
						if err != nil {
							t.Fatal(err)
						}
						_, got = PrepareMatrixDatabase(ctx, path, 256<<20, 1<<20, fi.Size(), fi.Size(), false, false, func(string) bool { return true }, func(string) bool { return false }, func(error) {}, func() { changed = true }, func(map[string]bool) { committed = true })
					case "export":
						got = CheckpointExportSQLite(ctx, path, 256<<20)
					}
				}()
				if !injected {
					t.Fatal("hook not reached")
				}
				if (got != nil) != (stage != "close") {
					t.Fatalf("error=%v stage=%s", got, stage)
				}
				if opened == nil {
					t.Fatal("never opened")
				}
				for stmt := range opened.Stmts() {
					t.Errorf("leaked statement: %s", stmt.SQL())
					stmt.Close()
				}
				if closes != 1 {
					t.Fatalf("close count=%d", closes)
				}
				if err := opened.Close(); err != nil {
					t.Fatal(err)
				}
				if closes != 1 {
					t.Fatal("connection left open")
				}
				if kind == "matrix" && (stage == "after_commit" || stage == "close") && (!committed || !changed) {
					t.Fatal("committed matrix lost its callback")
				}
			})
		}
	}
}

func TestSchemaPreparationStepPanicClosesStatements(t *testing.T) {
	for _, query := range []string{
		"SELECT tbl_name FROM sqlite_schema WHERE type='index' AND name=?",
		`PRAGMA index_info("sh_visitor_notes")`,
		`UPDATE "notes" SET approved_at = ?`,
		"PRAGMA page_size", "PRAGMA max_page_count",
	} {
		t.Run(query, func(t *testing.T) {
			conn, err := sqlite3.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			if err = conn.Exec("CREATE TABLE notes(id INTEGER PRIMARY KEY, visitor_id TEXT)"); err != nil {
				t.Fatal(err)
			}
			hit := false
			if err = conn.Trace(sqlite3.TRACE_STMT, func(_ sqlite3.TraceEvent, _, sql any) error {
				if sql == query {
					hit = true
					panic("injected")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if p := recover(); p != "injected" {
						t.Errorf("panic=%v", p)
					}
				}()
				switch {
				case strings.Contains(query, "approved_at"):
					err = EnsureHoldTables(conn, func(string) bool { return true })
				case strings.HasPrefix(query, "PRAGMA page") || query == "PRAGMA max_page_count":
					err = SetLegacySQLiteLimit(conn, 1<<20)
				default:
					err = EnsureOwnTables(conn, map[string]bool{"notes": true}, nil)
				}
			}()
			if !hit {
				t.Fatal("step not reached")
			}
			for stmt := range conn.Stmts() {
				t.Errorf("statement leaked: %s", stmt.SQL())
				stmt.Close()
			}
			if err = conn.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
