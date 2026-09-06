package handler

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

func TestDomainResponseDeadline(t *testing.T) {
	bound := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, status string
		verified     bool
		expires      string
	}{
		{"unproven pending", "pending", false, "2026-09-07T12:00:00Z"},
		{"unproven error", "error", false, ""},
		{"verified pending", "pending", true, ""},
		{"verified active", "active", true, ""},
		{"previously verified error", "error", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := domainResponse{Domain: "www.example.com", Status: tc.status}
			setDomainTimes(&resp, db.SiteDomainInfo{
				Status:     tc.status,
				BoundAt:    sql.NullTime{Time: bound, Valid: true},
				VerifiedAt: sql.NullTime{Time: bound, Valid: tc.verified},
			})
			data, err := json.Marshal(resp)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			if body["bound_at"] != "2026-09-06T12:00:00Z" {
				t.Fatalf("bound_at: %s", data)
			}
			expires, present := body["expires_at"]
			if tc.expires == "" {
				if present {
					t.Fatalf("unexpected expires_at: %s", data)
				}
			} else if expires != tc.expires {
				t.Fatalf("expires_at: %s", data)
			}
			if _, present := body["verified_at"]; present != tc.verified {
				t.Fatalf("verified_at: %s", data)
			}
		})
	}
}
