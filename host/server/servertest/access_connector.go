package servertest

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// ---- the whole flow against Postgres ---------------------------------------------

type ConnectorApp struct {
	Srv      *httptest.Server
	Database *sql.DB
	Admin    string
	Conn     *accesshttp.ConnectorHandler
	Sites    *siteshttp.SiteHandler
	Access   *accesshttp.AccessHandler
	Data     *appstorage.Handler
	Users    *accesshttp.UserHandler
	Mux      *http.ServeMux
}

// newConnectorApp is the server of cmd/server/main.go (newPrivateApp).
func NewConnectorApp(t *testing.T, eds ...*edition.Edition) *ConnectorApp {
	t.Helper()
	return NewPrivateApp(t, eds...).ConnectorApp
}

type Person struct{ Email, Key string }

func (a *ConnectorApp) NewPerson(t *testing.T, label string) Person {
	t.Helper()
	p := routetest.NewPerson(t, a.Database, label)
	return Person{Email: p.Email, Key: p.Key}
}

type Resp struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r Resp) JSON(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("not JSON (%d): %s", r.Status, r.Body)
	}
	return out
}

func (a *ConnectorApp) Do(t *testing.T, method, path string, body io.Reader, headers map[string]string) Resp {
	t.Helper()
	req, err := http.NewRequest(method, a.Srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return Resp{res.StatusCode, res.Header, b}
}

func JSONBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

func (a *ConnectorApp) Form(t *testing.T, path string, v url.Values, headers map[string]string) Resp {
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	for k, val := range headers {
		h[k] = val
	}
	return a.Do(t, http.MethodPost, path, strings.NewReader(v.Encode()), h)
}

func NewVerifier() (string, string) {
	v, _ := auth.GenerateAPIKey() // 64 hex chars: a valid verifier
	sum := sha256.Sum256([]byte(v))
	return v, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (a *ConnectorApp) RegisterClient(t *testing.T, redirect string) string {
	t.Helper()
	r := a.Do(t, http.MethodPost, "/oauth/register", JSONBody(map[string]any{
		"client_name": "Test Chat", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none",
	}), map[string]string{"Content-Type": "application/json"})
	if r.Status != http.StatusCreated {
		t.Fatalf("register: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)["client_id"].(string)
}

var ConnectDataRe = regexp.MustCompile(`<script type="application/json" id="connect-data">(.*?)</script>`)

// consent runs what the consent page does: load /oauth/authorize, then post
// the decision with the person's key. It returns the redirect the browser
// would follow.
func (a *ConnectorApp) Consent(t *testing.T, q url.Values, key, decision string) string {
	t.Helper()
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if page.Status != http.StatusOK {
		t.Fatalf("authorize page: %d %s", page.Status, page.Body)
	}
	m := ConnectDataRe.FindSubmatch(page.Body)
	if m == nil {
		t.Fatal("consent page carries no request data")
	}
	var data map[string]any
	_ = json.Unmarshal(m[1], &data)
	r := a.Do(t, http.MethodPost, "/oauth/authorize/decision", JSONBody(map[string]string{
		"query": q.Encode(), "csrf": data["csrf"].(string), "decision": decision,
	}), map[string]string{"Content-Type": "application/json", "X-API-Key": key, "Origin": a.Srv.URL})
	if r.Status != http.StatusOK {
		t.Fatalf("decision: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)["redirect_to"].(string)
}

func AuthorizeQuery(clientID, redirect, challenge string) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st4te"}, "scope": {"sites"},
	}
}

func CodeFrom(t *testing.T, redirectTo string) string {
	t.Helper()
	u, _ := url.Parse(redirectTo)
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in %s", redirectTo)
	}
	return code
}

// connect runs the whole flow for one person and returns the token response.
func (a *ConnectorApp) Connect(t *testing.T, p Person, clientID, redirect string) map[string]any {
	t.Helper()
	verifier, challenge := NewVerifier()
	q := AuthorizeQuery(clientID, redirect, challenge)
	q.Set("resource", a.Srv.URL+"/mcp")
	code := CodeFrom(t, a.Consent(t, q, p.Key, "allow"))
	r := a.Form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {a.Srv.URL + "/mcp"},
	}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("token: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)
}

func (a *ConnectorApp) Rpc(t *testing.T, token, method string, params any) Resp {
	t.Helper()
	return a.Do(t, http.MethodPost, "/mcp", JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token, "MCP-Protocol-Version": "2025-06-18"})
}

func ToolResultOf(t *testing.T, r Resp) (string, map[string]any, bool) {
	t.Helper()
	res, ok := r.JSON(t)["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %d %s", r.Status, r.Body)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	s, _ := res["structuredContent"].(map[string]any)
	return text, s, res["isError"].(bool)
}

const TestRedirect = "https://chat.example.com/oauth/callback"

// consentPage, decideConsent, exchangeCode, mcpToolNames and hasTool below
// moved out of hack_connector_test.go to host/hackhttp (cleanup 3.2
// slice 3), where they now have a *teamSiteApp receiver. This package's own
// TestConnectorHostIgnoresEventManagementMode still needs them, so they get
// their own *connectorApp copy here (the two types are no longer the same
// package, so one definition can no longer serve both).
func (a *ConnectorApp) ConsentPage(t *testing.T, q url.Values) (string, string) {
	t.Helper()
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if page.Status != http.StatusOK {
		t.Fatalf("authorize page: %d %s", page.Status, page.Body)
	}
	m := ConnectDataRe.FindSubmatch(page.Body)
	if m == nil {
		t.Fatal("consent page carries no request data")
	}
	var data map[string]any
	if err := json.Unmarshal(m[1], &data); err != nil {
		t.Fatal(err)
	}
	return string(page.Body), data["csrf"].(string)
}

func (a *ConnectorApp) DecideConsent(t *testing.T, q url.Values, csrf, key string, extra map[string]any) Resp {
	t.Helper()
	body := map[string]any{"query": q.Encode(), "csrf": csrf, "decision": "allow"}
	for k, v := range extra {
		body[k] = v
	}
	return a.Do(t, http.MethodPost, "/oauth/authorize/decision", JSONBody(body), map[string]string{
		"Content-Type": "application/json", "X-API-Key": key, "Origin": a.Srv.URL,
	})
}

func (a *ConnectorApp) ExchangeCode(t *testing.T, clientID, redirect, verifier, code, resource string) map[string]any {
	t.Helper()
	r := a.Form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
	}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("token: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)
}
