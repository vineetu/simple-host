package accounts

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/repository/accounts"
)

func TestInternalVisitorHasNoOwnerAuthority(t *testing.T) {
	key, revoke, err := accounts.IssueInternalVisitorKey(accounts.InternalVisitorPass{UserID: "person", SiteID: "one", Host: "one.example", AppName: "ChatGPT"})
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	// Nil DB proves rejection happens before loading any account identity.
	if _, err := New(nil).GetUserByAPIKey(context.Background(), key, 180*24*time.Hour); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("owner lookup: %v", err)
	}
}
