package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		{"severity":"warn","settings":["SESSION_IDLE"],"message":"Shorter idle.","suggest":{"SESSION_IDLE":"45m"}},
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
		{Severity: "warn", Settings: []string{"SESSION_IDLE"}, Message: "Shorter idle.", Suggest: map[string]string{"SESSION_IDLE": "45m"}},
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
