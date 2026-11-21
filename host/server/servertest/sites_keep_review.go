package servertest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
)

func KeepReviewSite(t *testing.T) (*ConnectorApp, *model.User) {
	t.Helper()
	a := NewConnectorApp(t)
	person := a.NewPerson(t, "keep-review")
	headers := map[string]string{"X-API-Key": person.Key, "Content-Type": "application/json"}
	for _, text := range []string{"version one", "version two"} {
		method := "PUT"
		if text == "version one" {
			method = "POST"
		}
		res := a.Do(t, method, "/v1/sites/blog/files", JSONBody(map[string]any{"files": map[string]string{"index.html": text, "about.html": text}}), headers)
		if res.Status < 200 || res.Status >= 300 {
			t.Fatal(res.Status, string(res.Body))
		}
	}
	user, err := accountsrepo.GetUserByUsername(context.Background(), a.Database, person.Email)
	if err != nil {
		t.Fatal(err)
	}
	return a, &user
}
func KeepReviewRequest(user *model.User, body io.Reader) *http.Request {
	r := httptest.NewRequest("PUT", "/v1/sites/blog/files?keep=1", body)
	r.SetPathValue("sitename", "blog")
	return r.WithContext(auth.WithUser(r.Context(), user))
}
