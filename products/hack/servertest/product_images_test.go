package servertest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/model"
	opsrepo "github.com/vsriram/simple-host/internal/repository/operations"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
	hackimages "github.com/vsriram/simple-host/products/hack/app/imagescan"
)

const imageOwnerID = "10000000-0000-0000-0000-000000000001"
const imageWriterID = "10000000-0000-0000-0000-000000000002"
const imageEventID = "10000000-0000-0000-0000-000000000003"
const imageTeamID = "10000000-0000-0000-0000-000000000004"

func productImageDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seedProductImage(t, db)
	return db
}

func seedProductImage(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`INSERT INTO users (id, username, handle, event_account) VALUES ('` + imageOwnerID + `','holding@events.invalid','image-event',true), ('` + imageWriterID + `','writer@example.com','image-writer',false)`,
		`INSERT INTO events (id, slug, account_id, title, organiser_name, contact_email, join_code, judge_code) VALUES ('` + imageEventID + `','image-event','` + imageOwnerID + `','Image event','Organiser','contact@example.com','image-join','image-judge')`,
		`INSERT INTO event_teams (id, event_id, slug, name, code) VALUES ('` + imageTeamID + `','` + imageEventID + `','image-team','Image team','image-team-code')`,
		`INSERT INTO event_entries (event_id, team_id, title, updated_by) VALUES ('` + imageEventID + `','` + imageTeamID + `','Keep this entry','` + imageWriterID + `')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
}

func productImageBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for i := range img.Pix {
		img.Pix[i] = byte((i*37 + i/7) % 256)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if len(b.Bytes()) < operations.ImageScanMinBytes {
		t.Fatal("fixture image too small")
	}
	return b.Bytes()
}

func storeProductImage(t *testing.T, db *sql.DB, kind string, raw []byte) model.ImageScanJob {
	t.Helper()
	job := model.ImageScanJob{Kind: kind, EventID: imageEventID, SHA256: operations.ImageHash(raw), Bytes: int64(len(raw)), Path: "icon"}
	var err error
	if kind == "event_icon" {
		_, err = db.Exec(`UPDATE events SET icon_bytes=$1, icon_media_type='image/png' WHERE id=$2`, raw, imageEventID)
	} else {
		job.TeamID, job.Path = imageTeamID, "screenshot"
		_, err = db.Exec(`UPDATE event_entries SET screenshot=$1, screenshot_type='image/png' WHERE team_id=$2`, raw, imageTeamID)
	}
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// These fixtures were captured from 693c385c before D2-S2 changed the scanner,
// using scripts/test-integration.sh and TEST_PG_URL. Only wall-clock
// timestamps and the temporary evidence directory are normalised. IDs, image
// bytes, contact details, step order, report metadata and writer fallback stay exact.
func TestProductImageOutputParity(t *testing.T) {
	for _, kind := range []string{"event_icon", "entry_screenshot"} {
		for _, writer := range []bool{true, false} {
			name := kind + "-contact"
			if writer {
				name = kind + "-writer"
			}
			t.Run(name, func(t *testing.T) {
				db := productImageDB(t)
				disk, err := storage.NewDiskStorage(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				f := newImageScanFixtureDB(t, db, disk)
				f.scan.Config.AutoReport = true
				raw := productImageBytes(t)
				job := storeProductImage(t, db, kind, raw)
				if writer {
					job.WriterID = imageWriterID
				}
				f.markMatch(job.SHA256)
				if _, err := opsrepo.EnqueueImageScan(t.Context(), db, job); err != nil {
					t.Fatal(err)
				}
				if n := f.scan.RunOnce(t.Context()); n != 1 {
					t.Fatalf("jobs: %d", n)
				}
				matches, err := opsrepo.ListImageMatches(t.Context(), db, 10)
				if err != nil || len(matches) != 1 {
					t.Fatalf("matches: %v %v", matches, err)
				}
				m := matches[0]
				record, err := os.ReadFile(filepath.Join(m.EvidencePath, "record.json"))
				if err != nil {
					t.Fatal(err)
				}
				if len(f.reporter.reports) != 1 {
					t.Fatalf("reports: %d", len(f.reporter.reports))
				}
				got, err := json.Marshal(map[string]any{"match": m, "record": json.RawMessage(record), "report": f.reporter.reports[0]})
				if err != nil {
					t.Fatal(err)
				}
				var value any
				if err := json.Unmarshal(got, &value); err != nil {
					t.Fatal(err)
				}
				value = normaliseProductImage(value, m.EvidencePath)
				got, err = json.MarshalIndent(value, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, '\n')
				filename := filepath.Join("testdata", "product-images", name+".json")
				want, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, got) {
					t.Fatalf("output changed from pre-extraction fixture %s\n%s", filename, got)
				}
			})
		}
	}
}

func normaliseProductImage(v any, evidence string) any {
	switch v := v.(type) {
	case map[string]any:
		for k, item := range v {
			v[k] = normaliseProductImage(item, evidence)
		}
	case []any:
		for i, item := range v {
			v[i] = normaliseProductImage(item, evidence)
		}
	case string:
		if at, err := time.Parse(time.RFC3339Nano, v); err == nil && !at.IsZero() {
			return "2026-01-02T03:04:05Z"
		}
		return strings.ReplaceAll(v, evidence, "<evidence>")
	}
	return v
}

func TestProductImagesMissingAndChanged(t *testing.T) {
	db := productImageDB(t)
	hook := hackimages.New(db, nil)
	for _, kind := range []string{"event_icon", "entry_screenshot"} {
		raw := productImageBytes(t)
		job := storeProductImage(t, db, kind, raw)
		if !hook.Owns(kind) || hook.Owns("site_file") {
			t.Fatal("incorrect owned kinds")
		}
		if got, err := hook.Load(t.Context(), db, job); err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("load: %v", err)
		}
		changed := append(append([]byte(nil), raw...), 1)
		storeProductImage(t, db, kind, changed)
		if cleared, err := hook.Clear(t.Context(), db, job, job.SHA256); err != nil || cleared {
			t.Fatalf("changed bytes cleared: %v %v", cleared, err)
		}
		if got, err := hook.Load(t.Context(), db, job); err != nil || !bytes.Equal(got, changed) {
			t.Fatalf("changed bytes lost: %v", err)
		}
		if cleared, err := hook.Clear(t.Context(), db, job, operations.ImageHash(changed)); err != nil || !cleared {
			t.Fatalf("matching bytes kept: %v %v", cleared, err)
		}
	}
	if _, err := db.Exec(`DELETE FROM events WHERE id=$1`, imageEventID); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"event_icon", "entry_screenshot"} {
		job := model.ImageScanJob{Kind: kind, EventID: imageEventID, TeamID: imageTeamID}
		if _, found, err := hook.Describe(t.Context(), db, job); err != nil || found {
			t.Fatalf("missing group: %v %v", found, err)
		}
		if raw, err := hook.Load(t.Context(), db, job); err != nil || raw != nil {
			t.Fatalf("missing image: %v %v", raw, err)
		}
	}
}

func TestProductImageEvidenceAndMarkerFailure(t *testing.T) {
	for _, kind := range []string{"event_icon", "entry_screenshot"} {
		for _, failure := range []string{"evidence", "marker"} {
			t.Run(kind+"-"+failure, func(t *testing.T) {
				db := productImageDB(t)
				disk, err := storage.NewDiskStorage(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				f := newImageScanFixtureDB(t, db, disk)
				markers := 0
				hook := hackimages.New(db, func(_ context.Context, ev model.Event) bool {
					markers++
					if ev.ID != imageEventID || ev.AccountID != imageOwnerID {
						t.Errorf("marker identity: %+v", ev)
					}
					assertProductTakenDown(t, db, true)
					return failure != "marker"
				})
				f.scan.SetProductImages(hook)
				if failure == "evidence" {
					// A regular file in place of the evidence directory fails even as root.
					blocked := filepath.Join(t.TempDir(), "blocked")
					if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
						t.Fatal(err)
					}
					f.scan.Config.EvidenceDir = blocked
				}
				raw := productImageBytes(t)
				job := storeProductImage(t, db, kind, raw)
				f.markMatch(job.SHA256)
				if _, err := opsrepo.EnqueueImageScan(t.Context(), db, job); err != nil {
					t.Fatal(err)
				}
				f.scan.RunOnce(t.Context())
				matches, err := opsrepo.ListImageMatches(t.Context(), db, 1)
				if err != nil || len(matches) != 1 {
					t.Fatalf("matches: %v %v", matches, err)
				}
				m := matches[0]
				got, err := hook.Load(t.Context(), db, job)
				if err != nil {
					t.Fatal(err)
				}
				assertProductTakenDown(t, db, true)
				if markers != 1 {
					t.Fatalf("marker calls: %d", markers)
				}
				if failure == "evidence" {
					if !bytes.Equal(got, raw) || !m.TakenDown || m.EvidencePath != "" {
						t.Fatalf("evidence failure lost image or missed take-down: %+v", m)
					}
				} else {
					if len(got) != 0 || m.TakenDown {
						t.Fatalf("marker failure must be reported failed: %+v", m)
					}
					record, err := os.ReadFile(filepath.Join(m.EvidencePath, "record.json"))
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Contains(record, []byte("team site markers could not all be written")) {
						t.Fatalf("missing marker failure: %s", record)
					}
				}
				sent := f.mailer.sent()
				if len(sent) != 1 || !strings.Contains(sent[0], map[string]string{"evidence": "take-down: done", "marker": "take-down: FAILED"}[failure]) {
					t.Fatalf("alert: %v", sent)
				}
			})
		}
	}
}

func assertProductTakenDown(t *testing.T, db *sql.DB, want bool) {
	t.Helper()
	var event, account bool
	if err := db.QueryRow(`SELECT e.taken_down_at IS NOT NULL,u.suspended_at IS NOT NULL FROM events e JOIN users u ON u.id=e.account_id WHERE e.id=$1`, imageEventID).Scan(&event, &account); err != nil {
		t.Fatal(err)
	}
	if event != want || account != want {
		t.Fatalf("event=%v account=%v want=%v", event, account, want)
	}
}

func TestProductImageTakeDownTransaction(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("suspension failure=", fail), func(t *testing.T) {
			db, rec := testdb.OpenRecording(testdb.Fresh(t))
			t.Cleanup(func() { db.Close() })
			seedProductImage(t, db)
			if fail {
				if _, err := db.Exec(`CREATE FUNCTION reject_suspension() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected suspension failure'; END $$; CREATE TRIGGER reject_suspension BEFORE UPDATE OF suspended_at ON users FOR EACH ROW EXECUTE FUNCTION reject_suspension()`); err != nil {
					t.Fatal(err)
				}
			}
			markers := 0
			hook := hackimages.New(db, func(context.Context, model.Event) bool {
				markers++
				statements := rec.Statements()
				if statements[len(statements)-1] != "COMMIT" {
					t.Error("markers before commit")
				}
				return true
			})
			rec.Reset()
			err := hook.TakeDown(t.Context(), model.ImageScanJob{EventID: imageEventID}, operations.ImageScanTakeDownMsg)
			if (err != nil) != fail {
				t.Fatalf("take-down error: %v", err)
			}
			got := rec.Statements()
			end := "COMMIT"
			wantMarkers := 1
			if fail {
				end = "ROLLBACK"
				wantMarkers = 0
			}
			wantStatements := 5
			if fail {
				wantStatements = 4
			}
			if !fail && (len(got) < 4 || !strings.HasPrefix(got[3], "DELETE FROM visitor_sessions")) {
				t.Fatalf("session revocation missing: %v", got)
			}
			if len(got) != wantStatements || got[0] != "BEGIN" || !strings.HasPrefix(strings.TrimSpace(got[1]), "UPDATE events SET") || !strings.HasPrefix(strings.TrimSpace(got[2]), "UPDATE users SET") || got[len(got)-1] != end {
				t.Fatalf("statement order: %v", got)
			}
			if markers != wantMarkers {
				t.Fatalf("marker calls: %d", markers)
			}
			assertProductTakenDown(t, db, !fail)
		})
	}
}

// Build must install the returned hook on the same scanner used by both the
// HTTP handlers and the in-process router used by MCP tools.
func TestProductImagesAfterBuildRESTAndMCP(t *testing.T) {
	for _, transport := range []string{"REST", "MCP"} {
		t.Run(transport, func(t *testing.T) {
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"Status":{"Code":3000,"Description":"OK"},"IsMatch":true,"TrackingId":"fixture-match"}`)
			}))
			t.Cleanup(provider.Close)
			cfg := platformtest.Config(t, testdb.Fresh(t))
			t.Setenv("PAPER_METRICS", "off")
			cfg.PhotoDNAKey = "fixture-only"
			cfg.PhotoDNAMatchURL = provider.URL
			cfg.PhotoDNAReportURL = provider.URL
			cfg.PhotoDNAAutoReport = false
			cfg.ImageScanAlertEmail = ""
			cfg.ImageScanEvidenceDir = t.TempDir()
			var db *sql.DB
			var disk *storage.DiskStorage
			var scan *operations.ImageScan
			ext := hack.Extension()
			wire := ext.Wire
			ext.Wire = func(d extension.Deps) (extension.Parts, error) {
				db, disk, scan = d.DB, d.Disk, d.ImageScan
				scan.Prepare = fakePrepare
				scan.Dispatch = func(fn func()) { fn() }
				return wire(d)
			}
			app := platformtest.Build(t, cfg, []*extension.Extension{ext})
			seedProductImage(t, db)
			if _, err := db.Exec(`INSERT INTO event_members(event_id,user_id,role) SELECT $1,id,'organiser' FROM users WHERE is_admin`, imageEventID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO sites(user_id,name) VALUES ($1,'image-team')`, imageOwnerID); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(disk.SiteDir(imageOwnerID, "image-team"), "current"), 0755); err != nil {
				t.Fatal(err)
			}
			raw := productImageBytes(t)
			rec := httptest.NewRecorder()
			if transport == "REST" {
				req := httptest.NewRequest("PUT", cfg.PublicBaseURL+"/v1/hack/events/image-event/icon", bytes.NewReader(raw))
				req.Header.Set("Content-Type", "image/png")
				req.Header.Set("X-API-Key", cfg.AdminAPIKey)
				app.Handler.ServeHTTP(rec, req)
			} else {
				// This is the same Upstream=bare mux arrangement as ConnectorHandler.
				rpc := mcp.NewServer(mcp.Config{Upstream: app.Mux, APIHost: cfg.SiteDomain, ServerName: "simple-hack", Version: "test"})
				body, err := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": 1, "method": "tools/call",
					"params": map[string]any{
						"_meta": map[string]any{
							"io.modelcontextprotocol/protocolVersion":    mcp.ProtocolVersion,
							"io.modelcontextprotocol/clientCapabilities": map[string]any{},
						},
						"name": "hack_set_event_icon",
						"arguments": map[string]any{
							"slug": "image-event", "content_type": "image/png",
							"image_base64": base64.StdEncoding.EncodeToString(raw),
						},
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header.Set("MCP-Protocol-Version", mcp.ProtocolVersion)
				req.Header.Set("Mcp-Method", "tools/call")
				req.Header.Set("Mcp-Name", "hack_set_event_icon")
				req = req.WithContext(mcp.WithCaller(req.Context(), mcp.Caller{APIKey: cfg.AdminAPIKey, Mode: mcp.CallerModeEvents}))
				rpc.ServeHTTP(rec, req)
			}
			if rec.Code != 200 || strings.Contains(rec.Body.String(), `"isError":true`) {
				t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
			}
			// A direct service call and the queued route both see the installed hook.
			if got, err := scan.LoadJobBytes(t.Context(), model.ImageScanJob{Kind: "event_icon", EventID: imageEventID, SHA256: operations.ImageHash(raw)}); err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("hook not installed: %v", err)
			}
			if n := scan.RunOnce(t.Context()); n != 1 {
				t.Fatalf("queued jobs: %d", n)
			}
			assertProductTakenDown(t, db, true)
			if !disk.IsSuspended(imageOwnerID, "image-team") {
				t.Fatal("product marker sync not wired")
			}
			matches, err := opsrepo.ListImageMatches(t.Context(), db, 1)
			if err != nil || len(matches) != 1 || !matches[0].TakenDown || matches[0].EventSlug != "image-event" {
				t.Fatalf("quarantine: %v %v", matches, err)
			}
		})
	}
}
