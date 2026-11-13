package hackhttp

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/lib/pq"
)

// Count actual SQL at the driver boundary, including queries from nested services.
type checklistQueryConnector struct {
	driver.Connector
	mu      sync.Mutex
	queries []string
}

func (c *checklistQueryConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return checklistQueryConn{conn, c}, nil
}

type checklistQueryConn struct {
	driver.Conn
	counter *checklistQueryConnector
}

func (c checklistQueryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.counter.mu.Lock()
	c.counter.queries = append(c.counter.queries, query)
	c.counter.mu.Unlock()
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}
func (c checklistQueryConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.counter.mu.Lock()
	c.counter.queries = append(c.counter.queries, query)
	c.counter.mu.Unlock()
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
func TestChecklistOrganiserEventQueryBound(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	counts := map[string]int{}
	for _, size := range []struct{ teams, judges int }{{2, 1}, {10, 4}} {
		connector, err := pq.NewConnector(dsn)
		if err != nil {
			t.Fatal(err)
		}
		counter := &checklistQueryConnector{Connector: connector}
		db := sql.OpenDB(counter)
		w := newJudgingWorldApp(t, newHackAppDatabase(t, db), size.teams, size.judges)
		// Include scoring judges so a per-scoring-judge regression is observable.
		for _, j := range w.judges {
			for _, team := range w.teams {
				if _, err := db.Exec(`INSERT INTO event_scores(event_id,judge_id,team_id,criterion_id,points) SELECT event_id,$2,$3,id,3 FROM rubric_criteria WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, j.id, team.id); err != nil {
					t.Fatal(err)
				}
			}
		}
		for _, stage := range []string{"draft", "closed", "results"} {
			// Use the real stage transition, which completes its one-time site pins.
			if r := w.call(t, "POST", "/stage", map[string]string{"stage": stage}, w.org); r.status != 200 {
				t.Fatalf("stage %s: %d %s", stage, r.status, r.body)
			}
			counter.mu.Lock()
			counter.queries = nil
			counter.mu.Unlock()
			r := w.call(t, "GET", "", nil, w.org)
			if r.status != 200 {
				t.Fatalf("event read %d %s", r.status, r.body)
			}
			if r.json(t)["checklist"] == nil {
				t.Fatal("missing checklist")
			}
			counter.mu.Lock()
			queries := append([]string{}, counter.queries...)
			counter.mu.Unlock()
			t.Logf("%s teams=%d judges=%d queries=%d", stage, size.teams, size.judges, len(queries))
			if len(queries) > 45 {
				t.Fatalf("%s: too many queries: %d", stage, len(queries))
			}
			if size.teams == 2 {
				counts[stage] = len(queries)
			} else if len(queries) > counts[stage] {
				t.Fatalf("%s query count grew: %d -> %d", stage, counts[stage], len(queries))
			}
			if stage == "draft" {
				for _, q := range queries {
					if strings.Contains(q, "FROM event_scores") || strings.Contains(q, "FROM event_assignments") || strings.Contains(q, "FROM event_former_judges f") {
						t.Fatalf("draft loaded judging facts: %s", q)
					}
				}
			}
		}
	}
}
