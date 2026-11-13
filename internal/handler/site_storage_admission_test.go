package handler

import (
	"net/http/httptest"
	"testing"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/config"
)

func TestStorageRequestDepsShareSiteSlots(t *testing.T) {
	h := &SiteHandler{limits: config.DefaultLimits()}
	first, second := h.storageDeps(), h.storageDeps()
	if first.SiteSlots == nil || first.SiteSlots != second.SiteSlots || first.SiteSlots != h.siteSlotsFor() {
		t.Fatal("request-built services must share site admission")
	}
}

func TestTableAdmissionErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"query_too_expensive", 422}, {"storage_failed", 500}, {"storage_busy", 503}} {
		t.Run(tc.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeTableError(w, &appstorage.Error{Code: tc.code, Message: tc.code})
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if tc.code == "storage_busy" && w.Header().Get("Retry-After") == "" {
				t.Fatal("missing Retry-After")
			}
		})
	}
}
