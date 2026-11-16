package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	repo "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func TestNoticeRecipients(t *testing.T) {
	db := admissionDB(t)
	ctx := context.Background()
	owner := func() (string, string) {
		t.Helper()
		var id string
		handle := convertHandle(t)
		if err := db.QueryRow(`INSERT INTO users(username,handle) VALUES($1,$2) RETURNING id`, handle+"@example.com", handle).Scan(&id); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, id) })
		return id, handle
	}
	site := func(owner string) string {
		t.Helper()
		var id string
		if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'notice-site') RETURNING id`, owner).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	filesOwner, _ := owner()
	filesSite := site(filesOwner)
	exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'photos','files')`, filesSite)
	converted, convertedHandle := owner()
	convertedSite := site(converted)
	exec(`INSERT INTO site_storage_resources(site_id,name,kind,rules,rules_converted_from) VALUES($1,'converted','sqlite','{"version":1,"policies":[]}','{}')`, convertedSite)
	exec(`INSERT INTO site_storage_resources(site_id,name,kind,acc_read,acc_add,acc_edit,acc_delete) VALUES($1,'new-preset','kv','own','signed-in','own','own')`, convertedSite)
	exec(`UPDATE sites SET state='{"old":true}' WHERE id=$1`, convertedSite)
	logsOwner, _ := owner()
	logsSite := site(logsOwner)
	heldOwner, _ := owner()
	heldSite := site(heldOwner)
	exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'held-preset','sqlite')`, heldSite)
	// The product's holding relationship, rather than users.event_account, is authoritative.
	group := func(_ context.Context, _ *sql.DB, id string) (string, error) {
		if id == heldOwner {
			return "notice-event", nil
		}
		return "", sql.ErrNoRows
	}
	templates, err := LoadNoticeTemplates(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	calls := "2026/11/16 legacy_storage_call site=" + logsSite + "\nlegacy_storage_call site=" + logsSite + "\n"
	report, err := ListNoticeRecipients(ctx, db, true, strings.NewReader(calls), group, templates, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	find := func(list []NoticeRecipient, id string) *NoticeRecipient {
		for i := range list {
			if list[i].AccountID == id {
				return &list[i]
			}
		}
		return nil
	}
	if find(report.Presets, filesOwner) != nil || find(report.Legacy, filesOwner) != nil {
		t.Fatal("file-only owner received a notice")
	}
	got := find(report.Presets, converted)
	if got == nil || len(got.Sites) != 1 || len(got.Sites[0].Resources) != 1 || got.Sites[0].Resources[0].Name != "new-preset" || got.Account != convertedHandle || got.Address != convertedHandle+"@example.com" {
		t.Fatalf("converted account's new preset missing: %+v", got)
	}
	if find(report.Legacy, converted) == nil {
		t.Fatal("converted account excluded from older-data list")
	}
	if got := find(report.Legacy, logsOwner); got == nil || len(got.Sites[0].Evidence) != 1 || got.Sites[0].Evidence[0] != "legacy_storage_call" {
		t.Fatalf("log-only site missing: %+v", got)
	}
	if find(report.Presets, heldOwner) != nil || find(report.Legacy, heldOwner) != nil {
		t.Fatal("holding account mailed")
	}
	found := false
	for _, h := range report.Held {
		if h.AccountID == heldOwner {
			found = h.Event == "notice-event" && h.Site.Resources[0].Name == "held-preset"
		}
	}
	if !found {
		t.Fatalf("event resource missing: %+v", report.Held)
	}
	count, err := repo.PresetNoticeCount(ctx, db)
	if err != nil || count != report.PresetResources {
		t.Fatalf("startup count %d != recipient resource count %d: %v", count, report.PresetResources, err)
	}
	withoutCalls, err := ListNoticeRecipients(ctx, db, true, nil, group, templates, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if find(withoutCalls.Legacy, converted) == nil || find(withoutCalls.Legacy, logsOwner) != nil {
		t.Fatal("stored legacy data must be listed without a log file; log-only sites need the log")
	}
	hackReport, err := ListNoticeRecipients(ctx, db, false, strings.NewReader(calls), group, templates, time.Time{})
	if err != nil || len(hackReport.Legacy) != 0 {
		t.Fatalf("product without legacy routes has recipients: %+v %v", hackReport.Legacy, err)
	}
}

func TestPresetSaveNotice(t *testing.T) {
	for _, kind := range []string{"sqlite", "kv", "files"} {
		t.Run(kind, func(t *testing.T) {
			s, call, in := rulesFixture(t, "CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT)")
			in.Call = call
			in.Kind = kind
			in.Name = "notice"
			in.Rules = nil
			in.Preset = "private"
			in.Tables = nil
			templates, err := LoadNoticeTemplates(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name    string
				enabled bool
				at      time.Time
				want    bool
			}{{"unset", true, time.Time{}, false}, {"flag-off", false, time.Now().Add(time.Hour), false}, {"notice-period", true, time.Now().Add(time.Hour), kind != "files"}, {"past", true, time.Now().Add(-time.Hour), kind != "files"}} {
				t.Run(tc.name, func(t *testing.T) {
					s.deps.RulesEnabled = tc.enabled
					s.deps.StorageSwitchAt = tc.at
					s.deps.PresetNotice = templates.Render(templates.SaveWarning, tc.at)
					got, err := s.PutStorageResource(context.Background(), in)
					if err != nil {
						t.Fatal(err)
					}
					if (len(got.Warnings) > 0) != tc.want {
						t.Fatalf("warnings=%v want=%v", got.Warnings, tc.want)
					}
				})
			}
			if kind != "files" {
				s.deps.RulesEnabled = true
				s.deps.KVStore = "sqlite"
				if kind == "kv" {
					_, err = s.db.Exec(`UPDATE site_storage_resources SET kv_store='sqlite' WHERE site_id=$1 AND name=$2`, call.SiteID, in.Name)
					if err != nil {
						t.Fatal(err)
					}
				}
				set := json.RawMessage(`{"policies":[]}`)
				in.Rules = &set
				in.Preset = ""
				s.deps.StorageSwitchAt = time.Now().Add(time.Hour)
				got, err := s.PutStorageResource(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Warnings) != 0 {
					t.Fatalf("rules save warned: %v", got.Warnings)
				}
			}
		})
	}
}

func TestNoticeEditableWording(t *testing.T) {
	dir := t.TempDir()
	override := NoticeTemplates{Presets: "approved draft <DATE>", Legacy: "approved older-data draft", SaveWarning: "approved warning <DATE>"}
	raw, _ := json.Marshal(override)
	if err := os.WriteFile(filepath.Join(dir, "storage-notice.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadNoticeTemplates(dir)
	if err != nil || got != override {
		t.Fatalf("override: %+v %v", got, err)
	}
}

func TestNoticeDescribesTableOverrides(t *testing.T) {
	m, _ := rules.PresetMatrix("comments")
	wall, _ := rules.PresetMatrix("wall")
	personal, _ := rules.PresetMatrix("personal")
	text := noticeAllows(rules.Resource{Kind: "sqlite", Access: &m, Tables: map[string]rules.TableRule{
		"public_wall":   {Matrix: wall},
		"private_notes": {Matrix: personal},
	}, ApproveFirst: true})
	for _, want := range []string{"read: any visitor", "add: visitors who pass the spam check", "table public_wall", "add: signed-in visitors", "table private_notes", "their own entries", "new visitor posts submitted through the spam check wait for your approval"} {
		if !strings.Contains(text, want) {
			t.Fatalf("description lacks %q: %s", want, text)
		}
	}
}

func TestWriteNoticeReportRejectsNonRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires a non-root process")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "recipients.json")
	const original = "existing report"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteNoticeReport(path, NoticeReport{}); err == nil || !strings.Contains(err.Error(), "root") {
		t.Fatalf("non-root report generation: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != original {
		t.Fatalf("rejected generation changed report: %q %v", raw, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("rejected generation left temporary files: %v %v", entries, err)
	}
}

func TestWriteNoticeReportRootOwnershipAndReplacement(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to verify root-owned report generation")
	}
	path := filepath.Join(t.TempDir(), "recipients.json")
	if err := WriteNoticeReport(path, NoticeReport{PresetResources: 1}); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer previous.Close()
	if err := WriteNoticeReport(path, NoticeReport{PresetResources: 2}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != 0 || info.Mode().Perm() != 0600 {
		t.Fatalf("recipient report must be root-owned mode 0600: %v", info)
	}
	oldInfo, err := previous.Stat()
	if err != nil || os.SameFile(info, oldInfo) {
		t.Fatalf("report was not atomically replaced: %v", err)
	}
	raw, err := os.ReadFile(path)
	var got NoticeReport
	if err != nil || json.Unmarshal(raw, &got) != nil || got.PresetResources != 2 {
		t.Fatalf("replacement contents: %s %v", raw, err)
	}
}
