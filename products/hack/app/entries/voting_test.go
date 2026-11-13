package entries

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"reflect"
	"testing"
	"time"

	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type votingDriver struct{}
type votingConn struct{ scenario string }
type votingRows struct {
	scenario string
	done     bool
}

func (votingDriver) Open(name string) (driver.Conn, error) { return votingConn{name}, nil }
func (c votingConn) Prepare(string) (driver.Stmt, error)   { return nil, driver.ErrSkip }
func (c votingConn) Close() error                          { return nil }
func (c votingConn) Begin() (driver.Tx, error)             { return nil, driver.ErrSkip }
func (c votingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &votingRows{scenario: c.scenario}, nil
}
func (r *votingRows) Columns() []string {
	return make([]string, len(eventrepo.ScanEventFields(&eventrepo.Event{})))
}
func (r *votingRows) Close() error { return nil }
func (r *votingRows) Next(values []driver.Value) error {
	if r.done || r.scenario == "missing" {
		return io.EOF
	}
	r.done = true
	fields := eventrepo.ScanEventFields(&eventrepo.Event{})
	for i, field := range fields {
		v := reflect.ValueOf(field)
		if v.Kind() != reflect.Ptr {
			values[i] = "{}"
			continue
		}
		switch v.Elem().Kind() {
		case reflect.String:
			values[i] = ""
		case reflect.Bool:
			values[i] = false
		case reflect.Int, reflect.Int64:
			values[i] = int64(0)
		case reflect.Struct:
			if v.Elem().Type() == reflect.TypeOf(time.Time{}) {
				values[i] = time.Now()
			} else {
				values[i] = nil
			}
		default:
			values[i] = "{}"
		}
	}
	values[0] = "event-id"
	if r.scenario == "empty_id" {
		values[0] = ""
	}
	values[5] = "open"
	if r.scenario == "draft" {
		values[5] = "draft"
	}
	if r.scenario == "taken_down" {
		values[32] = time.Now()
	}
	if r.scenario == "partial" {
		values[1] = nil
	}
	return nil
}

func init() { sql.Register("voting-load-signal", votingDriver{}) }

func TestVotingEventLoadSignal(t *testing.T) {
	for _, tc := range []struct {
		scenario        string
		loaded, refusal bool
	}{
		{"partial", false, false}, {"missing", false, true}, {"draft", true, true}, {"taken_down", true, true}, {"visible", true, false}, {"empty_id", true, false},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			database, err := sql.Open("voting-load-signal", tc.scenario)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			ev, loaded, err := New(database, nil).VotingEvent(context.Background(), "event")
			if loaded != tc.loaded {
				t.Fatalf("loaded = %v, event ID = %q, error = %v", loaded, ev.ID, err)
			}
			refusal, ok := err.(*Error)
			if tc.refusal != ok || (ok && refusal.Code != "event_not_found") {
				t.Fatalf("error = %v", err)
			}
			if tc.scenario == "partial" && (err == nil || ev.ID != "event-id") {
				t.Fatalf("partial event = %+v, error = %v", ev, err)
			}
			if tc.scenario == "visible" && err != nil {
				t.Fatal(err)
			}
		})
	}
}
