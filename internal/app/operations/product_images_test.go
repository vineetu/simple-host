package operations

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/imagescan"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/operations"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/testdb"
)

type imageHookStub struct {
	fail  string
	found bool
	calls []string
}

func (*imageHookStub) Owns(kind string) bool { return kind == "test_product" }
func (h *imageHookStub) call(method string) error {
	h.calls = append(h.calls, method)
	if h.fail == method {
		return errors.New("injected " + method + " failure")
	}
	return nil
}
func (h *imageHookStub) Load(context.Context, sqlkit.Querier, model.ImageScanJob) ([]byte, error) {
	return nil, h.call("load")
}
func (h *imageHookStub) Describe(context.Context, sqlkit.Querier, model.ImageScanJob) (ProductImageOwner, bool, error) {
	return ProductImageOwner{}, h.found, h.call("describe")
}
func (h *imageHookStub) Clear(context.Context, sqlkit.Querier, model.ImageScanJob, string) (bool, error) {
	return true, h.call("clear")
}
func (h *imageHookStub) TakeDown(context.Context, model.ImageScanJob, string) error {
	return h.call("take-down")
}

func TestProductImagesMissingHookWorkerContinues(t *testing.T) {
	db, recording := testdb.OpenRecording(testdb.Fresh(t))
	t.Cleanup(func() { db.Close() })
	s := NewImageScan(db, nil, &lifecycleMatcher{}, nil, nil, ImageScanConfig{EvidenceDir: t.TempDir()})
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
	// The next platform job names a pruned site, and must still finish even
	// though both product jobs fail to load without their hook.
	for _, kind := range []string{"event_icon", "entry_screenshot", "site_file"} {
		job := model.ImageScanJob{Kind: kind, SHA256: kind, SiteID: "10000000-0000-0000-0000-000000000001"}
		if _, err := repo.EnqueueImageScan(t.Context(), db, job); err != nil {
			t.Fatal(err)
		}
	}
	recording.Reset()
	if n := s.RunOnce(t.Context()); n != 3 {
		t.Fatalf("processed %d jobs", n)
	}
	rows, err := db.Query(`SELECT kind,attempts,last_error FROM image_scan_queue ORDER BY kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	for rows.Next() {
		var kind, last string
		var attempts int
		if err := rows.Scan(&kind, &attempts, &last); err != nil {
			t.Fatal(err)
		}
		if attempts != 1 || !strings.Contains(last, `unknown kind "`+kind+`"`) {
			t.Fatalf("retry %s %d %s", kind, attempts, last)
		}
		if !strings.Contains(logs.String(), last) {
			t.Fatalf("failure not logged: %s", last)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("remaining jobs: %d", n)
	}
	for _, table := range []string{"events", "event_entries", "event_teams"} {
		if recording.Names(table) {
			t.Fatalf("nil hook queried %s", table)
		}
	}
}

func TestProductImagesLoadError(t *testing.T) {
	failure := &imageHookStub{fail: "load"}
	s := NewImageScan(nil, nil, &lifecycleMatcher{}, nil, nil, ImageScanConfig{})
	s.SetProductImages(failure)
	if _, err := s.LoadJobBytes(t.Context(), model.ImageScanJob{Kind: "test_product"}); err == nil || err.Error() != "injected load failure" {
		t.Fatalf("load: %v", err)
	}
	if _, err := s.LoadJobBytes(t.Context(), model.ImageScanJob{Kind: "not_owned"}); err == nil {
		t.Fatal("unowned kind accepted")
	}
	var disabled *ImageScan
	disabled.SetProductImages(failure)
}

func TestProductImagesQuarantineErrors(t *testing.T) {
	const eventID = "10000000-0000-0000-0000-000000000009"
	for _, tc := range []struct {
		name, fail string
		found      bool
		wantCalls  []string
		failedStep string
		// The failed step's detail and error, and the log line for a group
		// that could not be read, are what the platform wrote before the hook.
		wantDetail, wantError, wantLog string
	}{
		{"not found", "", false, []string{"describe", "clear"}, "take-down", "the event row is missing", "", "image scan: event " + eventID + ": sql: no rows in result set"},
		{"describe error", "describe", true, []string{"describe", "clear"}, "take-down", "the event row is missing", "", "image scan: event " + eventID + ": injected describe failure"},
		{"clear error", "clear", true, []string{"describe", "clear", "take-down"}, "file removal", "", "injected clear failure", ""},
		{"take-down error", "take-down", true, []string{"describe", "clear", "take-down"}, "take-down", "the event", "injected take-down failure", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("postgres", testdb.Fresh(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			hook := &imageHookStub{found: tc.found, fail: tc.fail}
			s := NewImageScan(db, nil, &lifecycleMatcher{}, nil, nil, ImageScanConfig{EvidenceDir: t.TempDir()})
			s.SetProductImages(hook)
			raw := []byte("fixture bytes")
			var logs bytes.Buffer
			old := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(old) })
			s.quarantine(t.Context(), imageFinding{job: model.ImageScanJob{Kind: "test_product", EventID: eventID, SHA256: ImageHash(raw)}, data: raw, contentType: "image/png", provider: "fake", match: imagescan.Match{IsMatch: true}})
			log.SetOutput(old)
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog+"\n") {
				t.Fatalf("log line %q missing:\n%s", tc.wantLog, logs.String())
			}
			if !reflect.DeepEqual(hook.calls, tc.wantCalls) {
				t.Fatalf("calls %v, want %v", hook.calls, tc.wantCalls)
			}
			matches, err := repo.ListImageMatches(t.Context(), db, 1)
			if err != nil || len(matches) != 1 {
				t.Fatalf("matches %v %v", matches, err)
			}
			record, err := os.ReadFile(filepath.Join(matches[0].EvidencePath, "record.json"))
			if err != nil {
				t.Fatal(err)
			}
			var out struct{ Steps []imageStep }
			if err := json.Unmarshal(record, &out); err != nil {
				t.Fatal(err)
			}
			var failed bool
			for _, step := range out.Steps {
				if step.Step == tc.failedStep {
					failed = step.Status == stepFailed && step.Detail == tc.wantDetail && step.Error == tc.wantError
				}
			}
			if !failed {
				t.Fatalf("failure not recorded as %q %q: %s", tc.wantDetail, tc.wantError, record)
			}
			if tc.failedStep == "take-down" && matches[0].TakenDown {
				t.Fatal("failed take-down marked successful")
			}
		})
	}
}
