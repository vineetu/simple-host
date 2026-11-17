package server

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
)

func TestNoticeStartupCountAndCommand(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	templates, err := appstorage.LoadNoticeTemplates(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	report, err := appstorage.ListNoticeRecipients(context.Background(), db, true, nil, nil, templates, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	if err = logStoragePresetNotice(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), fmt.Sprintf("%d SQLite and key-value resources", report.PresetResources)) {
		t.Fatalf("startup count differs from report: %s", logs.String())
	}
	out := filepath.Join(t.TempDir(), "recipients.json")
	code := runMigrateRulesCommand([]string{"--list-recipients", "--out", out})
	if os.Geteuid() != 0 {
		if code != 1 {
			t.Fatalf("non-root recipient report exit %d, want 1", code)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("non-root command created a report: %v", err)
		}
		return
	}
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	raw, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(raw), `"held_by_an_event"`) {
		t.Fatalf("report missing: %v", err)
	}
}
