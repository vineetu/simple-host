package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/vsriram/simple-host/internal/capacity"
	"github.com/vsriram/simple-host/internal/tarball"
)

// siteLimitKey is where the per-site cap chosen during setup is persisted, so a
// restart does not quietly return the instance to the 100 MB default and let a
// participant fill a disk that was sized for 2 MB sites.
const siteLimitKey = "site_mb"

// SetSiteLimit applies an instance's per-site cap, in bytes, to every place that
// enforces one: the upload body cap and the extractor's uncompressed caps.
//
// Both, not either. The upload cap alone bounds the compressed bytes, and a
// well-compressed archive is an order of magnitude smaller than what it expands
// to — the extractor is where a 2 MB upload becoming 200 MB on disk is stopped.
// Call once at startup, before serving.
func SetSiteLimit(bytes int64) {
	if bytes <= 0 {
		return
	}
	maxSiteArchiveSize = bytes
	tarball.SetSiteLimit(bytes)
}

// SiteLimit reports the per-site cap currently in force, in bytes.
func SiteLimit() int64 { return maxSiteArchiveSize }

// LoadSiteLimit reads the persisted per-site cap in bytes. It returns zero when
// setup never chose one, which leaves the compiled-in default in place.
func LoadSiteLimit(ctx context.Context, db *sql.DB) (int64, error) {
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM instance_config WHERE key=$1`, siteLimitKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	siteMB, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || siteMB <= 0 {
		return 0, nil
	}
	return capacity.Plan{SiteMB: siteMB}.SiteBytes(), nil
}

// AutoSiteLimit sizes an instance from its own disk, for boxes that never see
// the setup page. An event server installed with its hostname already known
// skips setup entirely, and without this it would keep the 100 MB default —
// five participants taking that literally fill a 25 GB disk between them.
//
// Opt-in, via MAX_ARCHIVE_MB=auto. A long-running general instance must not
// have its limits moved underneath it by an upgrade, so the default is still to
// change nothing.
func AutoSiteLimit(dataDir string) (capacity.Plan, bool) {
	if os.Getenv("MAX_ARCHIVE_MB") != "auto" {
		return capacity.Plan{}, false
	}
	total, available, err := capacity.Disk(dataDir)
	if err != nil {
		return capacity.Plan{}, false
	}
	return capacity.ForPeople(total, available, 0), true
}

// capacityPlan answers "how many people fit on this server, and how big may
// each site be" from the real filesystem, not from a guess. Admin-only: it
// describes the machine, and the headcount of an event is not participant
// business.
//
// GET /v1/admin/capacity?people=120
func (h *SiteHandler) capacityPlan(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	people, _ := strconv.Atoi(r.URL.Query().Get("people"))
	total, available, err := capacity.Disk(h.disk.DataDir())
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not read this server's free space"})
		return
	}
	plan := capacity.ForPeople(total, available, people)
	writeJSON(w, 200, map[string]any{
		"plan":         plan,
		"fits":         plan.Fits(),
		"in_force_mb":  SiteLimit() >> 20,
		"max_accounts": maxBulkAccounts,
	})
}
