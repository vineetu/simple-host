package siteshttp

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

type unreadPhotoBody struct{ t *testing.T }

func (b unreadPhotoBody) Read([]byte) (int, error) {
	b.t.Fatal("busy request read its body")
	return 0, nil
}
func (b unreadPhotoBody) Close() error { return nil }

func TestPhotoSaveConcurrency(t *testing.T) {
	for _, limit := range []int{3, 1} {
		t.Setenv("PHOTO_MAX_CONCURRENT", fmt.Sprint(limit))
		for i := 0; i < limit; i++ {
			if !AcquirePhotoSave() {
				t.Fatal("slot refused")
			}
		}
		func() {
			defer PhotoSaves.Add(-int64(limit))
			// No DB or auth context: overload must stop before either and before reading.
			h := withHosts(&SiteHandler{})
			req := httptest.NewRequest("POST", "/v1/sites/home/photos", unreadPhotoBody{t})
			w := httptest.NewRecorder()
			h.AddPhotos(w, req)
			if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), "Busy, try again in a moment") {
				t.Fatalf("busy response: %d", w.Code)
			}
		}()
		if !AcquirePhotoSave() {
			t.Fatal("slot not released")
		}
		PhotoSaves.Add(-1)
	}
}
