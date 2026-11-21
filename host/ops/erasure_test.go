package ops

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sites"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/testdb"
)

type erasureStub struct {
	fail  string
	err   error
	tx    *sql.Tx
	calls []string
	swept bool
}

func (h *erasureStub) step(tx *sql.Tx, name string) error {
	h.calls = append(h.calls, name)
	if tx != nil {
		if h.tx != nil && h.tx != tx {
			return errors.New("hook transaction changed")
		}
		h.tx = tx
	}
	if h.fail == name {
		return h.err
	}
	return nil
}
func (h *erasureStub) Holds(_ context.Context, q sqlkit.Querier, _ string) (bool, error) {
	tx, _ := q.(*sql.Tx)
	return false, h.step(tx, "holds")
}
func (h *erasureStub) Block(_ context.Context, tx *sql.Tx, _ string, _ bool) (string, string, error) {
	return "", "", h.step(tx, "block")
}
func (h *erasureStub) Before(_ context.Context, tx *sql.Tx, _ string) ([]string, error) {
	return []string{"group"}, h.step(tx, "before")
}
func (h *erasureStub) Rows(_ context.Context, tx *sql.Tx, _ string) error { return h.step(tx, "rows") }
func (h *erasureStub) After(_ context.Context, tx *sql.Tx, _ []string) ([]string, error) {
	return []string{"holding"}, h.step(tx, "after")
}
func (h *erasureStub) Sweep(context.Context, *sql.DB, []string, func(context.Context, string, string) error) {
	h.swept = true
}
func (*erasureStub) HoldingText(string) string { return "holding account" }

func TestErasureNilAndErrors(t *testing.T) {
	for _, stage := range []string{"nil", "rows", "success"} {
		t.Run(stage, func(t *testing.T) {
			testdb.DB(t) // skips without a database
			db, rec := testdb.OpenRecording(os.Getenv("DB_DSN"))
			t.Cleanup(func() { db.Close() })
			var id string
			handle := "erasure-" + stage
			if err := db.QueryRow(`INSERT INTO users(username,handle,event_account) VALUES ($1,$2,true) RETURNING id`, handle+"@example.com", handle).Scan(&id); err != nil {
				t.Fatal(err)
			}
			want := errors.New("injected erasure error")
			h := &erasureStub{fail: stage, err: want}
			hooks := access.GroupHooks{}
			if stage != "nil" {
				hooks.Erasure = h
			}
			access.SetGroupHooks(hooks)
			t.Cleanup(func() { access.SetGroupHooks(access.GroupHooks{}) })
			rec.Reset()
			_, err := Cutoff{DB: db, Files: sites.New(db, nil, nil, 0, sites.Deps{})}.ErasePerson(context.Background(), access.DeleteAccountInput{ID: id, Confirm: handle})
			statements := rec.Statements()
			if stage == "nil" || stage == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if stage == "success" {
					if !strings.HasSuffix(strings.Join(statements, "\n"), "COMMIT") {
						t.Fatal("missing commit")
					}
					if strings.Join(h.calls, ",") != "holds,block,before,rows,after" {
						t.Fatal(h.calls)
					}
					if !h.swept {
						t.Fatal("sweep hook was not called after the commit")
					}
				} else {
					// Scan the complete authoritative product object list, not just events.
					b, e := os.ReadFile("../../scripts/split/hack-objects.txt")
					if e != nil {
						t.Fatal(e)
					}
					for _, line := range strings.Split(string(b), "\n") {
						fields := strings.Fields(line)
						if len(fields) > 2 && !strings.HasPrefix(fields[0], "#") && rec.Names(fields[2]) {
							t.Fatalf("nil erasure queried %s", fields[2])
						}
					}
				}
			} else {
				if !errors.Is(err, want) {
					t.Fatalf("error=%v", err)
				}
				if h.swept {
					t.Fatal("sweep on failure")
				}
				if statements[len(statements)-1] != "ROLLBACK" {
					t.Fatal("missing rollback")
				}
			}
			t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, id) })
			var exists bool
			if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, id).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != (stage != "nil" && stage != "success") {
				t.Fatalf("account exists=%t after %s", exists, stage)
			}
		})
	}
}
