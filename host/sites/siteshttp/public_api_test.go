package siteshttp

import (
	"github.com/vsriram/simple-host/host/web"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHostServesPublicSpec(t *testing.T) {
	web.SetInstanceHosts("simple-host.app", "", "")
	defer web.SetInstanceHosts("simple-host.app", "", "")
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(platformTestEdition()))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest("GET", "/hack-llms.txt", nil))
	if recorder.Code != http.StatusNotFound {
		t.Error("raw Hack listing exposed on Host")
	}
	var first string
	for _, path := range []string{"/openapi.json", "/openapi.yaml"} {
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatalf("%s: %d", path, r.Code)
		}
		if strings.Contains(r.Body.String(), "/v1/admin/") || strings.Contains(r.Body.String(), "/v1/hack/") || strings.Contains(r.Body.String(), "/v1/events") {
			t.Errorf("%s: mixed reference", path)
		}
		if first != "" && first != r.Body.String() {
			t.Error("JSON/YAML reference drift")
		}
		first = r.Body.String()
	}
}
