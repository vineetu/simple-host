package servertest

import (
	"encoding/json"
	"net/http"
	"testing"
)

func (a *ConnectorApp) ReviewerSignIn(t *testing.T, email, password string, headers map[string]string) Resp {
	t.Helper()
	h := map[string]string{"Content-Type": "application/json", "Origin": a.Srv.URL}
	for k, v := range headers {
		h[k] = v
	}
	return a.Do(t, http.MethodPost, "/oauth/reviewer-signin", JSONBody(map[string]string{"email": email, "password": password}), h)
}

func (a *ConnectorApp) AuthorizeData(t *testing.T, clientID string) map[string]any {
	t.Helper()
	_, challenge := NewVerifier()
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+AuthorizeQuery(clientID, TestRedirect, challenge).Encode(), nil, nil)
	m := ConnectDataRe.FindSubmatch(page.Body)
	if page.Status != http.StatusOK || m == nil {
		t.Fatalf("authorize page: %d", page.Status)
	}
	var data map[string]any
	_ = json.Unmarshal(m[1], &data)
	return data
}
