package connector_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"simplehost/access/connector"
	"simplehost/access/signin"
	"simplehost/app"
	"simplehost/core/testkit"
	"simplehost/core/web"
)

func whoami(a *app.App) {
	a.Routes.Platform.Handle("GET /test/me", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte(web.CallerOf(r).ID))
		return nil
	}))
}

const (
	base     = "https://simple-host.app"
	redirect = "https://claude.ai/api/mcp/auth_callback"
	form     = "application/x-www-form-urlencoded"
)

func tokens(t *testing.T, app *http.Client, v url.Values) (access, refresh string) {
	t.Helper()
	r := testkit.Call(t, app, "POST", base+"/oauth/token", v.Encode(), "Content-Type", form)
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	json.Unmarshal([]byte(r.Body), &out)
	if r.Status != 200 || !strings.HasPrefix(out.AccessToken, "shat_") || !strings.HasPrefix(out.RefreshToken, "shrt_") {
		t.Fatalf("token: %d %s", r.Status, r.Body)
	}
	return out.AccessToken, out.RefreshToken
}

// SPEC test 32 (F23): an app registers, the person approves, and its calls
// act as that person. Disconnect ends it.
func TestConnectAnApp(t *testing.T) {
	k := testkit.Start(t, signin.Mount, connector.Mount, whoami)
	nina := k.Person(t, "nina")
	app := k.Browser() // the AI app's own calls
	me := func(token string) testkit.Res {
		return testkit.Call(t, app, "GET", base+"/test/me", nil, "Authorization", "Bearer "+token)
	}

	r := testkit.Call(t, app, "POST", base+"/oauth/register", map[string]any{"client_name": "Claude",
		"redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none"})
	var reg struct {
		ID string `json:"client_id"`
	}
	if json.Unmarshal([]byte(r.Body), &reg); r.Status != 201 || !strings.HasPrefix(reg.ID, "shc_") {
		t.Fatalf("register: %d %s", r.Status, r.Body)
	}
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	ask := url.Values{"response_type": {"code"}, "client_id": {reg.ID}, "redirect_uri": {redirect}, "state": {"s1"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}

	// The person signs in, then sees the approval page.
	b := k.Browser()
	if r := testkit.Call(t, b, "GET", base+"/oauth/authorize?"+ask.Encode(), nil); r.Status != http.StatusFound || !strings.HasPrefix(r.Header.Get("Location"), "/signin?next=") {
		t.Fatalf("authorize signed out: %d %s", r.Status, r.Header.Get("Location"))
	}
	testkit.Call(t, b, "POST", base+"/v1/auth/code", map[string]string{"email": nina.Email})
	code := regexp.MustCompile(`\d{6}`).FindString(k.Mail.To(nina.Email)[0].Text)
	testkit.Call(t, b, "POST", base+"/v1/auth/verify", map[string]string{"email": nina.Email, "code": code})
	if r := testkit.Call(t, b, "GET", base+"/oauth/authorize?"+ask.Encode(), nil); r.Status != 200 || !strings.Contains(r.Body, "Connect Claude") {
		t.Fatalf("approval page: %d %s", r.Status, r.Body)
	}
	approve := url.Values{"decision": {"approve"}}
	for _, key := range []string{"client_id", "redirect_uri", "state", "code_challenge"} {
		approve.Set(key, ask.Get(key))
	}
	r = testkit.Call(t, b, "POST", base+"/oauth/authorize", approve.Encode(), "Content-Type", form)
	back, _ := url.Parse(r.Header.Get("Location"))
	if r.Status != http.StatusFound || !strings.HasPrefix(back.String(), redirect) || back.Query().Get("state") != "s1" {
		t.Fatalf("approve: %d %s", r.Status, back)
	}

	// The app trades the code for tokens, and its calls act as nina.
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {back.Query().Get("code")}, "redirect_uri": {redirect},
		"client_id": {reg.ID}, "code_verifier": {verifier}}
	access, refresh := tokens(t, app, exchange)
	if got := me(access).Body; got != nina.ID {
		t.Fatalf("app acts as %q, want %q", got, nina.ID)
	}
	if r := testkit.Call(t, app, "POST", base+"/oauth/token", exchange.Encode(), "Content-Type", form); r.Status != 400 {
		t.Fatalf("code used twice: %d %s", r.Status, r.Body)
	}
	access, _ = tokens(t, app, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {reg.ID}})
	if got := me(access).Body; got != nina.ID {
		t.Fatalf("refreshed token acts as %q", got)
	}

	// Disconnecting ends it at once.
	apps, err := connector.Apps(context.Background(), k.App.DB, nina.ID)
	if err != nil || len(apps) != 1 || apps[0].Name != "Claude" {
		t.Fatalf("connected apps: %+v %v", apps, err)
	}
	if r := testkit.Call(t, b, "DELETE", base+"/v1/me/apps/"+apps[0].ID, nil); r.Status != 200 {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	if r := me(access); r.Status != 401 {
		t.Fatalf("token after disconnect: %d %s", r.Status, r.Body)
	}
}
