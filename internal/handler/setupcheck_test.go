package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func newTestSetupCheck(t *testing.T, f *fakeSidecar, o AskOptions) (*AskHandler, http.Handler) {
	t.Helper()
	if o.Every == 0 {
		o.Every = 20 * time.Second
	}
	if o.Burst == 0 {
		o.Burst = 100
	}
	if o.MaxInFlight == 0 {
		o.MaxInFlight = 4
	}
	if o.DailyMax == 0 {
		o.DailyMax = 100
	}
	srv := f.server(t)
	h := newAskHandler("sidecar-key", srv.URL+"/v1", "grok-test", testApex+"/", &askMemCounter{}, o)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/ask", h.ask)
	mux.HandleFunc("POST /v1/setup/check", h.setupCheck)
	return h, mux
}

func postCheck(mux http.Handler, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/setup/check", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", testApex)
	r.RemoteAddr = "198.51.100.7:4321"
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

// Requests the check refuses: the wrong origin or type, anything but
// {product, settings}, an unknown product or setting, any secret, free text,
// and values the setting does not allow. None reaches the model or uses a
// daily slot.
func TestSetupCheckValidation(t *testing.T) {
	f := &fakeSidecar{answer: `{"findings":[]}`}
	h, mux := newTestSetupCheck(t, f, AskOptions{SetupCheckDailyMax: 100})
	good := `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":"500"}}`
	cases := []struct {
		name, body string
		mod        func(*http.Request)
		status     int
		code       string
	}{
		{"other origin", good, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403, "forbidden_origin"},
		{"a hosted site", good, func(r *http.Request) { r.Header.Set("Origin", "https://x.vineetu.simple-host.app") }, 403, "forbidden_origin"},
		{"no origin", good, func(r *http.Request) { r.Header.Del("Origin") }, 403, "forbidden_origin"},
		{"form post", good, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, 415, "unsupported_media_type"},
		{"extra field", `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":"500"},"email":"a@b.c"}`, nil, 400, "invalid_body"},
		{"not JSON", `product=small-box`, nil, 400, "invalid_body"},
		{"number value", `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":500}}`, nil, 400, "invalid_body"},
		{"unknown product", `{"product":"other","settings":{"MAX_ARCHIVE_MB":"500"}}`, nil, 400, "unknown_product"},
		{"no settings", `{"product":"small-box","settings":{}}`, nil, 400, "invalid_settings"},
		{"unknown setting", `{"product":"small-box","settings":{"NOPE":"1"}}`, nil, 400, "unknown_setting"},
		{"other product's setting", `{"product":"small-box","settings":{"UPLOAD_CONCURRENCY":"4"}}`, nil, 400, "unknown_setting"},
		{"small-box secret", `{"product":"small-box","settings":{"RESEND_API_KEY":"re_x"}}`, nil, 400, "secret_not_accepted"},
		{"small-box secret beside a good one", `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":"500","ADMIN_API_KEY":"k"}}`, nil, 400, "secret_not_accepted"},
		{"enterprise secret", `{"product":"enterprise","settings":{"OIDC_CLIENT_SECRET":"s"}}`, nil, 400, "secret_not_accepted"},
		{"enterprise secret, empty", `{"product":"enterprise","settings":{"SESSION_SIGNING_KEY":""}}`, nil, 400, "secret_not_accepted"},
		{"free text", `{"product":"small-box","settings":{"SITE_DOMAIN":"hack.example.com"}}`, nil, 400, "setting_not_checkable"},
		{"enterprise free text", `{"product":"enterprise","settings":{"ADMIN_EMAILS":"a@example.com"}}`, nil, 400, "setting_not_checkable"},
		{"below range", `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":"0"}}`, nil, 400, "invalid_value"},
		{"above range", `{"product":"small-box","settings":{"SIGNIN_CODE_TTL_MINUTES":"61"}}`, nil, 400, "invalid_value"},
		{"not a number", `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":"lots"}}`, nil, 400, "invalid_value"},
		{"not a choice", `{"product":"small-box","settings":{"WRITE_AUTH_MODE":"maybe"}}`, nil, 400, "invalid_value"},
		{"looser than the loosest", `{"product":"small-box","settings":{"RATE_LIMIT_SIGNIN_IP":"500,1s"}}`, nil, 400, "invalid_value"},
		{"wrong rate separator", `{"product":"enterprise","settings":{"RATE_LIMIT_STATE_CLIENT":"60,1s"}}`, nil, 400, "invalid_value"},
		{"enterprise duration over max", `{"product":"enterprise","settings":{"SESSION_TTL":"25h"}}`, nil, 400, "invalid_value"},
		{"negative duration", `{"product":"enterprise","settings":{"CLAMD_TIMEOUT":"-5s"}}`, nil, 400, "invalid_value"},
		{"two lines", `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":"5\n6"}}`, nil, 400, "invalid_value"},
	}
	for _, c := range cases {
		w := postCheck(mux, c.body, c.mod)
		if w.Code != c.status || askCode(t, w) != c.code {
			t.Errorf("%s: got %d %s, want %d %s", c.name, w.Code, w.Body.String(), c.status, c.code)
		}
	}
	if len(f.bodies) != 0 {
		t.Fatalf("refused requests reached the model: %d", len(f.bodies))
	}
	if n := h.checkDaily.(*askMemCounter).count; n != 0 {
		t.Fatalf("refused requests used %d daily slots", n)
	}
	// Values each product allows get through.
	for _, body := range []string{
		good,
		`{"product":"small-box","settings":{"RATE_LIMIT_SIGNIN_IP":"10,10s","WRITE_AUTH_MODE":"on","IDLE_CLEANUP":"on","SIGNIN_CODE_TTL_MINUTES":"10"}}`,
		`{"product":"enterprise","settings":{"MAX_ARCHIVE_BYTES":"524288000","UPLOAD_CONCURRENCY":"8","SESSION_TTL":"12h","RATE_LIMIT_STATE_CLIENT":"30/2s","SECURE_MODE":"true","DB_PORT":"6543"}}`,
	} {
		if w := postCheck(mux, body, nil); w.Code != http.StatusOK {
			t.Errorf("%s: got %d %s", body, w.Code, w.Body.String())
		}
	}
}

// The model's findings are checked against the registry: unknown or secret
// settings, bad severities, empty messages and suggestions outside the
// allowed range drop the whole finding. A fenced reply still parses.
func TestSetupCheckFindingsValidated(t *testing.T) {
	reply := "```json\n" + `{"findings":[
		{"severity":"warn","settings":["MAX_ARCHIVE_BYTES","UPLOAD_CONCURRENCY"],"message":"8 uploads of **500 MB** need about 4 GB.","suggest":{"UPLOAD_CONCURRENCY":"2"}},
		{"severity":"info","setting":"SESSION_TTL","message":"Longer than most identity providers."},
		{"severity":"warn","settings":["UPLOAD_CONCURRENCY"],"message":"Too high.","suggest":{"UPLOAD_CONCURRENCY":"999"}},
		{"severity":"warn","settings":["MAX_ARCHIVE_BYTES"],"message":"Too big.","suggest":{"MAX_ARCHIVE_BYTES":"1"}},
		{"severity":"warn","settings":["OIDC_CLIENT_SECRET"],"message":"Rotate it."},
		{"severity":"warn","settings":["SESSION_TTL"],"message":"Set the key.","suggest":{"SESSION_SIGNING_KEY":"k1:abc"}},
		{"severity":"warn","settings":["NOT_A_SETTING"],"message":"Made up."},
		{"severity":"warn","settings":["ADMIN_EMAILS"],"message":"Free text."},
		{"severity":"critical","settings":["SESSION_TTL"],"message":"Wrong severity."},
		{"severity":"info","settings":["SESSION_TTL"],"message":"   "},
		{"severity":"warn","settings":["SESSION_IDLE"],"message":"Shorter idle.","suggest":{"SESSION_IDLE":"20m"}},
		{"severity":"warn","settings":["RATE_LIMIT_AUTH_CLIENT"],"message":"Loosest.","suggest":{"RATE_LIMIT_AUTH_CLIENT":"500/1s"}}
	]}` + "\n```"
	f := &fakeSidecar{answer: reply}
	_, mux := newTestSetupCheck(t, f, AskOptions{SetupCheckDailyMax: 10, ReasoningEffort: "low"})
	w := postCheck(mux, `{"product":"enterprise","settings":{"MAX_ARCHIVE_BYTES":"524288000","UPLOAD_CONCURRENCY":"8","SESSION_TTL":"24h"}}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("check: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Findings []setupFinding `json:"findings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []setupFinding{
		{Severity: "warn", Settings: []string{"MAX_ARCHIVE_BYTES", "UPLOAD_CONCURRENCY"}, Message: "8 uploads of 500 MB need about 4 GB.", Suggest: map[string]string{"UPLOAD_CONCURRENCY": "2"}},
		{Severity: "info", Settings: []string{"SESSION_TTL"}, Message: "Longer than most identity providers."},
		{Severity: "warn", Settings: []string{"SESSION_IDLE"}, Message: "Shorter idle.", Suggest: map[string]string{"SESSION_IDLE": "20m"}},
	}
	gb, _ := json.Marshal(got.Findings)
	wb, _ := json.Marshal(want)
	if string(gb) != string(wb) {
		t.Fatalf("findings:\n got %s\nwant %s", gb, wb)
	}
	// What the model was sent: the model and effort, the rules and registry,
	// and the settings with their defaults. Never a secret's description line,
	// nothing about the visitor.
	var up struct {
		Model           string          `json:"model"`
		ReasoningEffort string          `json:"reasoning_effort"`
		Messages        []openAIMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(f.bodies[0]), &up); err != nil {
		t.Fatal(err)
	}
	if up.Model != "grok-test" || up.ReasoningEffort != "low" || len(up.Messages) != 2 {
		t.Fatalf("upstream request: model %q effort %q, %d messages", up.Model, up.ReasoningEffort, len(up.Messages))
	}
	sys, user := up.Messages[0].Content, up.Messages[1].Content
	for _, want := range []string{"UPLOAD_CONCURRENCY | int | 2 | 1..64 uploads", "MAX_ARCHIVE_BYTES × UPLOAD_CONCURRENCY", "proxy-body-size", "SESSION_IDLE must be shorter than SESSION_TTL", "| security |", "JSON only"} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	for _, secret := range []string{"OIDC_CLIENT_SECRET |", "SESSION_SIGNING_KEY |", "DB_PASSWORD |"} {
		if strings.Contains(sys, secret) {
			t.Errorf("system prompt lists the secret %q", secret)
		}
	}
	if user != `{"changed":[{"name":"MAX_ARCHIVE_BYTES","value":"524288000","default":"104857600"},{"name":"SESSION_TTL","value":"24h","default":"8h"},{"name":"UPLOAD_CONCURRENCY","value":"8","default":"2"}],"product":"enterprise"}` {
		t.Errorf("user message = %s", user)
	}
	for _, leak := range []string{"198.51.100", "sh_session", "Mozilla"} {
		if strings.Contains(f.bodies[0], leak) {
			t.Errorf("upstream request carries %q", leak)
		}
	}
	for k := range f.headers[0] {
		if k != "Authorization" && k != "Content-Type" && k != "Content-Length" && k != "Accept-Encoding" && k != "User-Agent" {
			t.Errorf("upstream request carries header %s", k)
		}
	}
}

// No findings is an empty list; a reply that is not the JSON asked for, or a
// failed model call, is a 502 the helper treats as "Check skipped".
func TestSetupCheckModelReplies(t *testing.T) {
	for answer, want := range map[string]int{
		`{"findings":[]}`:                 200,
		`Looks fine! {"findings": []} :)`: 200,
		`Everything looks fine.`:          502,
		`{"findings":"none"}`:             502,
	} {
		f := &fakeSidecar{answer: answer}
		_, mux := newTestSetupCheck(t, f, AskOptions{SetupCheckDailyMax: 10})
		w := postCheck(mux, `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`, nil)
		if w.Code != want {
			t.Errorf("answer %q: got %d %s, want %d", answer, w.Code, w.Body.String(), want)
		}
		if want == 200 && strings.TrimSpace(w.Body.String()) != `{"findings":[]}` {
			t.Errorf("answer %q: body %s", answer, w.Body.String())
		}
	}
	f := &fakeSidecar{status: 500}
	_, mux := newTestSetupCheck(t, f, AskOptions{SetupCheckDailyMax: 10})
	if w := postCheck(mux, `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`, nil); w.Code != http.StatusBadGateway || askCode(t, w) != "unavailable" {
		t.Errorf("model failure: %d %s", w.Code, w.Body.String())
	}
}

// Its own daily cap (SETUP_CHECK_DAILY_MAX; 0 answers none), and the Ask
// per-address limit it shares.
func TestSetupCheckCaps(t *testing.T) {
	body := `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`
	f := &fakeSidecar{answer: `{"findings":[]}`}
	h, mux := newTestSetupCheck(t, f, AskOptions{SetupCheckDailyMax: 2, DailyMax: 50})
	for i := 0; i < 2; i++ {
		if w := postCheck(mux, body, nil); w.Code != http.StatusOK {
			t.Fatalf("check %d: %d", i, w.Code)
		}
	}
	w := postCheck(mux, body, nil)
	if w.Code != http.StatusTooManyRequests || askCode(t, w) != "daily_limit" {
		t.Fatalf("over the daily cap: %d %s", w.Code, w.Body.String())
	}
	if n := h.daily.(*askMemCounter).count; n != 0 {
		t.Fatalf("checks used %d of the Ask questions", n)
	}
	// The Ask questions have their own count.
	if w := postAsk(mux, `{"question":"q","page":"features"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("ask after the checks ran out: %d", w.Code)
	}

	_, off := newTestSetupCheck(t, &fakeSidecar{answer: `{"findings":[]}`}, AskOptions{SetupCheckDailyMax: 0})
	if w := postCheck(off, body, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("SETUP_CHECK_DAILY_MAX=0: %d", w.Code)
	}

	_, limited := newTestSetupCheck(t, &fakeSidecar{answer: `{"findings":[]}`}, AskOptions{SetupCheckDailyMax: 10, Burst: 1})
	if w := postCheck(limited, body, nil); w.Code != http.StatusOK {
		t.Fatalf("first: %d", w.Code)
	}
	if w := postCheck(limited, body, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("per-address limit: %d", w.Code)
	}
	if w := postAsk(limited, `{"question":"q","page":"features"}`, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the check and Ask share the per-address limit: %d", w.Code)
	}
}

// Like /v1/ask: no CORS grant, and nothing kept in the API metrics.
func TestSetupCheckNoCORSNoMetrics(t *testing.T) {
	_, mux := newTestSetupCheck(t, &fakeSidecar{answer: `{"findings":[]}`}, AskOptions{SetupCheckDailyMax: 10})
	body := `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`
	w := postCheck(CORS(mux), body, nil)
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("check: %d, ACAO %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	r := httptest.NewRequest(http.MethodOptions, "/v1/setup/check", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	pw := httptest.NewRecorder()
	CORS(mux).ServeHTTP(pw, r)
	if pw.Header().Get("Access-Control-Allow-Origin") != "" || pw.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("preflight was granted: %v", pw.Header())
	}
	m := &APIMetrics{routes: map[routeKey]int64{}, ips: map[string]*ipAgg{}}
	if w := postCheck(m.Wrap(mux), body, nil); w.Code != http.StatusOK {
		t.Fatalf("check: %d", w.Code)
	}
	if len(m.routes) != 0 || len(m.ips) != 0 {
		t.Fatalf("the check was counted: %v", m.routes)
	}
}

// Every setting in both lists validates its own default (so the registry and
// the validator agree), and every secret is refused.
func TestSetupRegistriesValidateDefaults(t *testing.T) {
	for _, product := range []string{"small-box", "enterprise"} {
		r := setupRegistryFor(product)
		if r == nil || len(r.list) < 50 {
			t.Fatalf("%s: registry not loaded", product)
		}
		for i := range r.list {
			s := &r.list[i]
			if !s.checkable() || strings.HasPrefix(s.Default, "<") || s.Default == "" {
				continue
			}
			if !r.valid(s, s.Default) {
				t.Errorf("%s: %s default %q does not validate", product, s.Name, s.Default)
			}
		}
	}
}

// checkFindings posts one check with the model answering reply, and returns
// the findings.
func checkFindings(t *testing.T, body, reply string) []setupFinding {
	t.Helper()
	_, mux := newTestSetupCheck(t, &fakeSidecar{answer: reply}, AskOptions{SetupCheckDailyMax: 10})
	w := postCheck(mux, body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("check: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Findings []setupFinding `json:"findings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got.Findings
}

// A suggestion never loosens a security-sensitive setting past both its
// default and the visitor's own value: the finding keeps its message and
// loses its suggestion. Stricter suggestions, and ones no looser than what
// the visitor already chose, are kept.
func TestSetupCheckNoLooserSuggestions(t *testing.T) {
	cases := []struct {
		name, product, sent, suggest string
		kept                         bool
	}{
		// Enterprise: insecure switches, strict choices, lifetimes, rates.
		{"insecure switch on", "enterprise", `"SESSION_TTL":"4h"`, `"OIDC_INSECURE_ALLOWED":"true"`, false},
		{"plaintext switch on", "enterprise", `"SESSION_TTL":"4h"`, `"BACKUP_ENVELOPE_PLAINTEXT_ALLOWED":"true"`, false},
		{"insecure switch off", "enterprise", `"DB_INSECURE_ALLOWED":"true"`, `"DB_INSECURE_ALLOWED":"false"`, true},
		{"secure mode off", "enterprise", `"SESSION_TTL":"4h"`, `"SECURE_MODE":"false"`, true}, // false is the default
		{"secure mode off after on", "enterprise", `"SECURE_MODE":"true"`, `"SECURE_MODE":"false"`, true},
		{"one approval", "enterprise", `"NETWORK_ACCESS_APPROVALS":"2"`, `"NETWORK_ACCESS_APPROVALS":"1"`, true}, // the default
		{"two approvals", "enterprise", `"SESSION_TTL":"4h"`, `"NETWORK_ACCESS_APPROVALS":"2"`, true},
		{"owner access log", "enterprise", `"SESSION_TTL":"4h"`, `"ACCESS_LOG_VISIBILITY":"owner"`, false},
		{"admin access log", "enterprise", `"ACCESS_LOG_VISIBILITY":"owner"`, `"ACCESS_LOG_VISIBILITY":"admin"`, true},
		{"longer session", "enterprise", `"SESSION_TTL":"12h"`, `"SESSION_TTL":"24h"`, false},
		{"session back to the default", "enterprise", `"SESSION_TTL":"12h"`, `"SESSION_TTL":"8h"`, true},
		{"session between", "enterprise", `"SESSION_TTL":"12h"`, `"SESSION_TTL":"10h"`, true},
		{"longer key days", "enterprise", `"API_KEY_MAX_DAYS":"180"`, `"API_KEY_MAX_DAYS":"365"`, true}, // 365 is the default
		{"longer default key days", "enterprise", `"SESSION_TTL":"4h"`, `"API_KEY_DEFAULT_DAYS":"180"`, false},
		{"looser rate", "enterprise", `"SESSION_TTL":"4h"`, `"RATE_LIMIT_AUTH_CLIENT":"40/5s"`, false},
		{"faster rate", "enterprise", `"SESSION_TTL":"4h"`, `"RATE_LIMIT_AUTH_CLIENT":"20/2s"`, false},
		{"stricter rate", "enterprise", `"SESSION_TTL":"4h"`, `"RATE_LIMIT_AUTH_CLIENT":"10/10s"`, true},
		{"rate as the visitor chose", "enterprise", `"RATE_LIMIT_AUTH_CLIENT":"40/5s"`, `"RATE_LIMIT_AUTH_CLIENT":"30/5s"`, true},
		// Small box.
		{"writes without sign-in", "small-box", `"KEEP_VERSIONS":"3"`, `"WRITE_AUTH_MODE":"off"`, false},
		{"writes need sign-in", "small-box", `"KEEP_VERSIONS":"3"`, `"WRITE_AUTH_MODE":"on"`, true},
		{"back to log", "small-box", `"WRITE_AUTH_MODE":"off"`, `"WRITE_AUTH_MODE":"log"`, true},
		{"shared data", "small-box", `"SAVED_DATA_DEFAULT_KIND":"declare_first"`, `"SAVED_DATA_DEFAULT_KIND":"shared"`, true}, // the default
		{"year-long visitor session", "small-box", `"KEEP_VERSIONS":"3"`, `"VISITOR_SESSION_DAYS":"365"`, false},
		{"year-long refresh", "small-box", `"OAUTH_REFRESH_TTL_DAYS":"120"`, `"OAUTH_REFRESH_TTL_DAYS":"365"`, false},
		{"shorter refresh", "small-box", `"OAUTH_REFRESH_TTL_DAYS":"120"`, `"OAUTH_REFRESH_TTL_DAYS":"100"`, true},
		{"looser sign-in rate", "small-box", `"KEEP_VERSIONS":"3"`, `"RATE_LIMIT_SIGNIN_IP":"40,5s"`, false},
		// Not security-sensitive: any valid value.
		{"more versions", "small-box", `"KEEP_VERSIONS":"3"`, `"KEEP_VERSIONS":"50"`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name := strings.Trim(strings.SplitN(c.suggest, ":", 2)[0], `"`)
			reply := `{"findings":[{"severity":"warn","settings":["` + name + `"],"message":"Look again.","suggest":{` + c.suggest + `}}]}`
			got := checkFindings(t, `{"product":"`+c.product+`","settings":{`+c.sent+`}}`, reply)
			if len(got) != 1 || got[0].Message != "Look again." {
				t.Fatalf("findings %+v: the message must stay", got)
			}
			if (got[0].Suggest != nil) != c.kept {
				t.Fatalf("suggest %s after %s: got %v, kept should be %v", c.suggest, c.sent, got[0].Suggest, c.kept)
			}
		})
	}
	// One looser value takes the whole suggestion away, not just its part.
	got := checkFindings(t, `{"product":"enterprise","settings":{"SESSION_TTL":"12h"}}`,
		`{"findings":[{"severity":"warn","settings":["SESSION_TTL"],"message":"Both.","suggest":{"SESSION_IDLE":"15m","SESSION_TTL":"20h"}}]}`)
	if len(got) != 1 || got[0].Suggest != nil {
		t.Fatalf("mixed suggestion: %+v", got)
	}
}

// Every security-sensitive switch and choice in both lists has a strict
// order, so none falls back to "the default or nothing" by accident.
func TestSetupRegistriesStrictOrder(t *testing.T) {
	for _, product := range []string{"small-box", "enterprise"} {
		r := setupRegistryFor(product)
		for i := range r.list {
			s := &r.list[i]
			if s.kind() == "choice" && s.Security && len(s.StrictOrder) != len(s.Allowed) {
				t.Errorf("%s: %s has strict order %v for %v", product, s.Name, s.StrictOrder, s.Allowed)
			}
		}
	}
	e := setupRegistryFor("enterprise")
	for _, n := range []string{"OIDC_INSECURE_ALLOWED", "DB_INSECURE_ALLOWED", "BACKUP_STORAGE_INSECURE_ALLOWED", "BACKUP_ENVELOPE_PLAINTEXT_ALLOWED"} {
		if s := e.by[n]; s == nil || !s.Security || len(s.StrictOrder) != 2 || s.StrictOrder[0] != "false" {
			t.Errorf("%s: not treated as an insecure switch: %+v", n, s)
		}
	}
}

// Values are printable ASCII: Unicode spaces and line separators around a
// rate's separator are refused, from the visitor and from the model, and a
// suggested rate or number comes back in one canonical form.
func TestSetupCheckASCIIAndCanonical(t *testing.T) {
	f := &fakeSidecar{answer: `{"findings":[]}`}
	_, mux := newTestSetupCheck(t, f, AskOptions{SetupCheckDailyMax: 100})
	for _, v := range []string{"5,\u000b1m", "5, 1m", "5 ,1m", "5, 1m", "5,\u00851m", "5,1µs", "５,1m"} {
		body, _ := json.Marshal(map[string]any{"product": "small-box", "settings": map[string]string{"RATE_LIMIT_UPLOAD": v}})
		if w := postCheck(mux, string(body), nil); w.Code != http.StatusBadRequest || askCode(t, w) != "invalid_value" {
			t.Errorf("rate %q: %d %s", v, w.Code, w.Body.String())
		}
	}
	if w := postCheck(mux, `{"product":"small-box","settings":{"MAX_ARCHIVE_MB":"２00"}}`, nil); w.Code != http.StatusBadRequest {
		t.Errorf("full-width digit: %d", w.Code)
	}
	if len(f.bodies) != 0 {
		t.Fatalf("a refused value reached the model")
	}
	got := checkFindings(t, `{"product":"small-box","settings":{"RATE_LIMIT_UPLOAD":"5, 1m"}}`,
		`{"findings":[
			{"severity":"info","settings":["RATE_LIMIT_UPLOAD"],"message":"Tidy.","suggest":{"RATE_LIMIT_UPLOAD":" 010 , 30s ","MAX_ARCHIVE_MB":"0200"}},
			{"severity":"info","settings":["RATE_LIMIT_UPLOAD"],"message":"Odd space.","suggest":{"RATE_LIMIT_UPLOAD":"10,\u000b30s"}}
		]}`)
	if len(got) != 1 || got[0].Suggest["RATE_LIMIT_UPLOAD"] != "10,30s" || got[0].Suggest["MAX_ARCHIVE_MB"] != "200" {
		t.Fatalf("canonical suggestions: %+v", got)
	}
	got = checkFindings(t, `{"product":"enterprise","settings":{"RATE_LIMIT_AUTH_CLIENT":"20 / 10s"}}`,
		`{"findings":[{"severity":"info","settings":["RATE_LIMIT_AUTH_CLIENT"],"message":"Tidy.","suggest":{"RATE_LIMIT_AUTH_CLIENT":"10 / 20s"}}]}`)
	if len(got) != 1 || got[0].Suggest["RATE_LIMIT_AUTH_CLIENT"] != "10/20s" {
		t.Fatalf("enterprise canonical rate: %+v", got)
	}
}

// Messages carry no links: markdown links become their label, bare
// addresses go, and a label that is itself an address goes too.
func TestSetupCheckMessagesHaveNoLinks(t *testing.T) {
	got := checkFindings(t, `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`,
		`{"findings":[{"severity":"info","settings":["KEEP_VERSIONS"],"message":"See [the docs](https://evil.example/x) or https://evil.example/y and [https://simple-host.app/setup](https://simple-host.app/setup), also www.evil.example today."}]}`)
	if len(got) != 1 {
		t.Fatalf("findings: %+v", got)
	}
	m := got[0].Message
	for _, bad := range []string{"http", "evil", "www.", "](", "simple-host.app"} {
		if strings.Contains(m, bad) {
			t.Errorf("message keeps %q: %q", bad, m)
		}
	}
	if !strings.Contains(m, "See the docs or") {
		t.Errorf("message lost its words: %q", m)
	}
}

// The check has its own in-flight slots (Ask busy does not stop it, and a
// check never takes one of Ask's), and a count per network per day that is
// handed back when the day's cap across everyone refuses the check.
func TestSetupCheckOwnSlotsAndNetworkCap(t *testing.T) {
	body := `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`
	h, mux := newTestSetupCheck(t, &fakeSidecar{answer: `{"findings":[]}`}, AskOptions{SetupCheckDailyMax: 100, SetupCheckPerNetworkDaily: 2, MaxInFlight: 1})
	if cap(h.checkInFlight) != 1 {
		t.Fatalf("default SETUP_CHECK_MAX_IN_FLIGHT: %d", cap(h.checkInFlight))
	}
	h.inFlight <- struct{}{} // Ask is full
	if w := postCheck(mux, body, nil); w.Code != http.StatusOK {
		t.Fatalf("check while Ask is full: %d %s", w.Code, w.Body.String())
	}
	<-h.inFlight
	h.checkInFlight <- struct{}{} // the check is full
	if w := postCheck(mux, body, nil); w.Code != http.StatusServiceUnavailable || askCode(t, w) != "busy" {
		t.Fatalf("second check at once: %d %s", w.Code, w.Body.String())
	}
	if w := postAsk(mux, `{"question":"q","page":"features"}`, nil); w.Code != http.StatusOK {
		t.Fatalf("ask while a check runs: %d", w.Code)
	}
	<-h.checkInFlight

	// Per network: 198.51.100.7 has used 1 of 2; .9 is the same /24.
	from := func(addr string) func(*http.Request) { return func(r *http.Request) { r.RemoteAddr = addr } }
	if w := postCheck(mux, body, from("198.51.100.9:1")); w.Code != http.StatusOK {
		t.Fatalf("second from the network: %d", w.Code)
	}
	if w := postCheck(mux, body, from("198.51.100.200:1")); w.Code != http.StatusTooManyRequests || askCode(t, w) != "daily_limit" {
		t.Fatalf("third from the network: %d %s", w.Code, w.Body.String())
	}
	if w := postCheck(mux, body, from("203.0.113.5:1")); w.Code != http.StatusOK {
		t.Fatalf("another network: %d", w.Code)
	}
	// A new UTC day starts the counts again.
	h.now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	if w := postCheck(mux, body, from("198.51.100.200:1")); w.Code != http.StatusOK {
		t.Fatalf("next day: %d", w.Code)
	}

	// Refused by the cap across everyone: the network's count is handed back.
	h2, mux2 := newTestSetupCheck(t, &fakeSidecar{answer: `{"findings":[]}`}, AskOptions{SetupCheckDailyMax: 1, SetupCheckPerNetworkDaily: 5})
	postCheck(mux2, body, nil)
	if w := postCheck(mux2, body, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("over the day's cap: %d", w.Code)
	}
	if n := h2.checkNet.n["198.51.100.0"]; n != 1 {
		t.Fatalf("network count after a refused check: %d, want 1", n)
	}
}

// The page (setup.js, setupKind) and the server (kind) classify every
// setting of both lists the same way, so what the page lets through is what
// the server checks.
func TestSetupKindMatchesPage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, err := staticFiles.ReadFile("static/setup/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	i, j := strings.Index(src, "// <setupKind>"), strings.Index(src, "// </setupKind>")
	if i < 0 || j < i {
		t.Fatal("setup.js lacks the <setupKind> block")
	}
	for _, product := range []string{"small-box", "enterprise"} {
		list, _ := staticFiles.ReadFile("static/setup/" + product + "-settings.json")
		prog := src[i:j] + "\nvar d = " + string(list) + ";\nvar out = {}; d.settings.forEach(function (s) { out[s.name] = setupKind(s); }); process.stdout.write(JSON.stringify(out));"
		cmd := exec.Command(node, "-e", "function numeric(x) { return typeof x === 'number'; }\n"+prog)
		b, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: node: %v", product, err)
		}
		var page map[string]string
		if err := json.Unmarshal(b, &page); err != nil {
			t.Fatal(err)
		}
		r := setupRegistryFor(product)
		if len(page) != len(r.list) {
			t.Fatalf("%s: page classified %d settings, server has %d", product, len(page), len(r.list))
		}
		for k := range r.list {
			s := &r.list[k]
			if page[s.Name] != s.kind() {
				t.Errorf("%s: %s is %q on the page, %q on the server", product, s.Name, page[s.Name], s.kind())
			}
		}
	}
	// The shape the review found: min 0 and no max is a number on both.
	s := &setupSetting{Type: "duration", Min: json.RawMessage("0"), Default: "5"}
	if s.kind() != "number" {
		t.Fatalf("min 0, no max: %s", s.kind())
	}
}
