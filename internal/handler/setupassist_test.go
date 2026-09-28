package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func newTestSetupAssist(t *testing.T, f *fakeSidecar, o AskOptions) (*AskHandler, http.Handler) {
	t.Helper()
	if o.SetupAssistDailyMax == 0 {
		o.SetupAssistDailyMax = 100
	}
	h, mux := newTestSetupCheck(t, f, o)
	m := mux.(*http.ServeMux)
	m.HandleFunc("POST /v1/setup/assist", h.setupAssist)
	return h, m
}

func postAssist(mux http.Handler, body string, mod func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/setup/assist", strings.NewReader(body))
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

// assistReply is the model's raw reply: text, the marker, the JSON.
func assistReply(text, changes string) string {
	return text + "\n" + setupAssistMarker + "\n" + changes
}

func decodeAssist(t *testing.T, w *httptest.ResponseRecorder) setupAssistReply {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("assist: %d %s", w.Code, w.Body.String())
	}
	var got setupAssistReply
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// Requests the assistant refuses: the wrong origin or type, unknown fields,
// an unknown product, step or area, the check's refusals for choices
// (unknown, secret, free text, out of range), basic answers that are not
// picked from a list or not one of its values, an empty or long message, a
// long paste. None reaches the model or uses a daily slot.
func TestSetupAssistValidation(t *testing.T) {
	f := &fakeSidecar{answer: assistReply("Fine.", `{"changes":[]}`)}
	h, mux := newTestSetupAssist(t, f, AskOptions{})
	good := `{"product":"small-box","step":"basics","message":"what is KEEP_VERSIONS?"}`
	long := strings.Repeat("x", setupAssistMaxMessage+1)
	paste, _ := json.Marshal(strings.Repeat("y", setupAssistMaxPasted+1))
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
		{"extra field", `{"product":"small-box","step":"basics","message":"q","email":"a@b.c"}`, nil, 400, "invalid_body"},
		{"not JSON", `message=hi`, nil, 400, "invalid_body"},
		{"unknown product", `{"product":"other","step":"basics","message":"q"}`, nil, 400, "unknown_product"},
		{"unknown step", `{"product":"small-box","step":"deploy","message":"q"}`, nil, 400, "invalid_step"},
		{"unknown mode", `{"product":"small-box","step":"basics","mode":"expert","message":"q"}`, nil, 400, "invalid_step"},
		{"other product's area", `{"product":"small-box","step":"advanced","area":"access","message":"q"}`, nil, 400, "invalid_step"},
		{"unknown setting", `{"product":"small-box","step":"files","choices":{"NOPE":"1"},"message":"q"}`, nil, 400, "unknown_setting"},
		{"secret", `{"product":"small-box","step":"files","choices":{"RESEND_API_KEY":"re_x"},"message":"q"}`, nil, 400, "secret_not_accepted"},
		{"enterprise secret", `{"product":"enterprise","step":"files","choices":{"SESSION_SIGNING_KEY":"k1:x"},"message":"q"}`, nil, 400, "secret_not_accepted"},
		{"free text", `{"product":"small-box","step":"files","choices":{"SITE_DOMAIN":"hack.example.com"},"message":"q"}`, nil, 400, "setting_not_checkable"},
		{"enterprise free text", `{"product":"enterprise","step":"files","choices":{"ADMIN_EMAILS":"a@example.com"},"message":"q"}`, nil, 400, "setting_not_checkable"},
		{"out of range", `{"product":"small-box","step":"files","choices":{"SIGNIN_CODE_TTL_MINUTES":"61"},"message":"q"}`, nil, 400, "invalid_value"},
		{"looser than the loosest", `{"product":"small-box","step":"files","choices":{"RATE_LIMIT_SIGNIN_IP":"500,1s"},"message":"q"}`, nil, 400, "invalid_value"},
		{"typed basic answer", `{"product":"small-box","step":"basics","basics":{"domain":"hack.example.com"},"message":"q"}`, nil, 400, "unknown_basic"},
		{"enterprise typed basic answer", `{"product":"enterprise","step":"basics","basics":{"admins":"a@example.com"},"message":"q"}`, nil, 400, "unknown_basic"},
		{"other product's basic", `{"product":"small-box","step":"basics","basics":{"idp":"okta"},"message":"q"}`, nil, 400, "unknown_basic"},
		{"basic not in its list", `{"product":"enterprise","step":"basics","basics":{"idp":"auth0"},"message":"q"}`, nil, 400, "invalid_value"},
		{"basic as a boolean", `{"product":"small-box","step":"basics","basics":{"codes":true},"message":"q"}`, nil, 400, "invalid_body"},
		{"empty message", `{"product":"small-box","step":"basics","message":"  "}`, nil, 400, "empty_question"},
		{"long message", `{"product":"small-box","step":"basics","message":"` + long + `"}`, nil, 400, "question_too_long"},
		{"long paste", `{"product":"small-box","step":"files","message":"help","pasted":` + string(paste) + `}`, nil, 400, "paste_too_long"},
	}
	for _, c := range cases {
		w := postAssist(mux, c.body, c.mod)
		if w.Code != c.status || askCode(t, w) != c.code {
			t.Errorf("%s: got %d %s, want %d %s", c.name, w.Code, w.Body.String(), c.status, c.code)
		}
	}
	if len(f.bodies) != 0 {
		t.Fatalf("refused requests reached the model: %d", len(f.bodies))
	}
	if n := h.assistDaily.(*askMemCounter).count; n != 0 {
		t.Fatalf("refused requests used %d daily slots", n)
	}
	// What each product accepts gets through.
	for _, body := range []string{
		good,
		`{"product":"small-box","step":"advanced","mode":"advanced","area":"accounts","choices":{"RATE_LIMIT_SIGNIN_IP":"10,10s","KEEP_VERSIONS":"1"},"basics":{"codes":"true","google":"false"},"message":"clean up","history":[{"q":"hi","a":"hello"}]}`,
		`{"product":"enterprise","step":"choose","basics":{"idp":"entra","certs":"manual","smtp":"true","bucket":"gcs","creds":"keys"},"message":"q","pasted":"FAILED: x"}`,
	} {
		if w := postAssist(mux, body, nil); w.Code != http.StatusOK {
			t.Errorf("%s: got %d %s", body, w.Code, w.Body.String())
		}
	}
}

// Every proposed change is checked against the registry and the helper's
// form: a setting the helper does not write (a basic question's, a secret,
// free text, or on a small box one Compose does not pass through), a value
// outside its range, a no-op, a duplicate, or a value that loosens a
// security-sensitive setting past both its default and the visitor's is
// dropped. Basic answers must be one of their list's values and a change.
func TestSetupAssistChangesValidated(t *testing.T) {
	reply := assistReply("For a 200-person company with **Microsoft** sign-in, see [docs](https://evil.example/x):", `{"changes":[
		{"setting":"SESSION_TTL","value":"4h","why":"A shorter working session."},
		{"setting":"SESSION_IDLE","value":"15m","why":"Ends unattended sessions sooner."},
		{"setting":"SESSION_TTL","value":"2h","why":"Duplicate."},
		{"setting":"API_KEY_MAX_DAYS","value":30,"why":"A number without quotes."},
		{"setting":"NETWORK_ACCESS_APPROVALS","value":"2","why":"Two admins approve."},
		{"setting":"ACCESS_LOG_VISIBILITY","value":"owner","why":"Looser than the default."},
		{"setting":"OAUTH_REFRESH_TTL","value":"8760h","why":"Longer than allowed or looser."},
		{"setting":"RATE_LIMIT_AUTH_CLIENT","value":"500/1s","why":"Looser than the loosest."},
		{"setting":"OIDC_CLIENT_SECRET","value":"x","why":"A secret."},
		{"setting":"ADMIN_EMAILS","value":"a@example.com","why":"Free text."},
		{"setting":"SECURE_MODE","value":"true","why":"A basic question's."},
		{"setting":"OIDC_INSECURE_ALLOWED","value":"true","why":"An insecure switch."},
		{"setting":"UPLOAD_CONCURRENCY","value":"999","why":"Out of range."},
		{"setting":"UPLOAD_CONCURRENCY","value":"3","why":"Same as now."},
		{"setting":"NOT_A_SETTING","value":"1","why":"Made up."},
		{"setting":"DELETED_RETENTION_DAYS","value":"14","why":"Visit https://evil.example today."}
	],"basics":{"idp":"entra","certs":"auto","bucket":"azure","domain":"x.example.com","smtp":true}}`)
	f := &fakeSidecar{answer: reply}
	_, mux := newTestSetupAssist(t, f, AskOptions{ReasoningEffort: "low"})
	got := decodeAssist(t, postAssist(mux, `{"product":"enterprise","step":"basics","mode":"basic","choices":{"UPLOAD_CONCURRENCY":"3"},"basics":{"idp":"okta","certs":"auto","smtp":"false"},"message":"Set this up for a 200-person company with Microsoft sign-in and stricter security"}`, nil))
	if got.Answer != "For a 200-person company with Microsoft sign-in, see docs:" {
		t.Errorf("answer = %q", got.Answer)
	}
	var names []string
	for _, c := range got.Changes {
		names = append(names, c.Setting+"="+c.Value)
	}
	want := []string{"SESSION_TTL=4h", "SESSION_IDLE=15m", "API_KEY_MAX_DAYS=30", "NETWORK_ACCESS_APPROVALS=2", "DELETED_RETENTION_DAYS=14"}
	if !slices.Equal(names, want) {
		t.Errorf("changes = %v, want %v", names, want)
	}
	for _, c := range got.Changes {
		if strings.Contains(c.Why, "http") || strings.Contains(c.Why, "evil") {
			t.Errorf("why keeps a link: %q", c.Why)
		}
	}
	if len(got.Basics) != 2 || got.Basics["idp"] != "entra" || got.Basics["smtp"] != "true" {
		t.Errorf("basics = %v, want idp=entra smtp=true (certs is no change, bucket and domain not allowed)", got.Basics)
	}

	// A small box: settings Compose does not pass through, and the basic
	// questions' own settings, are not offered.
	f = &fakeSidecar{answer: assistReply("Here.", `{"changes":[
		{"setting":"WRITE_AUTH_MODE","value":"on","why":"Not on a small box."},
		{"setting":"SITE_DOMAIN","value":"x.example.com","why":"Typed."},
		{"setting":"MAIL_FROM","value":"a <a@b.c>","why":"Typed."},
		{"setting":"KEY_IDLE_EXPIRY_DAYS","value":"0","why":"Never is looser."},
		{"setting":"KEY_IDLE_EXPIRY_DAYS","value":"90","why":"Stricter."},
		{"setting":"RATE_LIMIT_SIGNIN_IP","value":"10, 10s","why":"Stricter, tidied."},
		{"setting":"MAX_SITES_PER_ACCOUNT","value":"20","why":"A cap per person."}
	],"basics":{"codes":"false","google":"true","idp":"entra"}}`)}
	_, mux = newTestSetupAssist(t, f, AskOptions{})
	got = decodeAssist(t, postAssist(mux, `{"product":"small-box","step":"basics","basics":{"codes":"true","google":"false"},"message":"stricter"}`, nil))
	names = nil
	for _, c := range got.Changes {
		names = append(names, c.Setting+"="+c.Value)
	}
	want = []string{"KEY_IDLE_EXPIRY_DAYS=90", "RATE_LIMIT_SIGNIN_IP=10,10s", "MAX_SITES_PER_ACCOUNT=20"}
	if !slices.Equal(names, want) {
		t.Errorf("small box changes = %v, want %v", names, want)
	}
	if len(got.Basics) != 2 || got.Basics["codes"] != "false" || got.Basics["google"] != "true" {
		t.Errorf("small box basics = %v", got.Basics)
	}

	// Past the Basics step, basic answers are dropped (the page could not
	// check the fields they need there); setting changes still come.
	for _, step := range []string{"advanced", "files"} {
		got = decodeAssist(t, postAssist(mux, `{"product":"small-box","step":"`+step+`","basics":{"codes":"true","google":"false"},"message":"stricter"}`, nil))
		if len(got.Basics) != 0 || len(got.Changes) != 3 {
			t.Errorf("step %s: basics = %v, %d changes; want no basics, 3 changes", step, got.Basics, len(got.Changes))
		}
	}
	f = &fakeSidecar{answer: assistReply("Go back to Basics to switch to Google.", `{"changes":[],"basics":{"idp":"google"}}`)}
	_, mux = newTestSetupAssist(t, f, AskOptions{})
	got = decodeAssist(t, postAssist(mux, `{"product":"enterprise","step":"files","basics":{"idp":"okta"},"message":"we use Google Workspace"}`, nil))
	if len(got.Basics) != 0 {
		t.Errorf("enterprise files step: basics = %v, want none", got.Basics)
	}
}

// The looser rule is the check's, applied against the visitor's current
// value: a value between the default and a looser current one is kept.
func TestSetupAssistNoLooserChanges(t *testing.T) {
	cases := []struct {
		name, product, choices, change string
		kept                           bool
	}{
		{"longer session", "enterprise", ``, `"SESSION_TTL","value":"12h"`, false},
		{"back toward the default", "enterprise", `"SESSION_TTL":"12h"`, `"SESSION_TTL","value":"10h"`, true},
		{"owner access log", "enterprise", ``, `"ACCESS_LOG_VISIBILITY","value":"owner"`, false},
		{"admin access log", "enterprise", ``, `"ACCESS_LOG_VISIBILITY","value":"admin"`, true},
		{"plaintext switch", "enterprise", ``, `"BACKUP_ENVELOPE_PLAINTEXT_ALLOWED","value":"true"`, false},
		{"insecure switch off", "enterprise", `"DB_INSECURE_ALLOWED":"true"`, `"DB_INSECURE_ALLOWED","value":"false"`, true},
		{"unbacked-up in-cluster database", "enterprise", ``, `"DB_INCLUSTER_EVALUATION","value":"true"`, false},
		{"in-cluster database off", "enterprise", `"DB_INCLUSTER_EVALUATION":"true"`, `"DB_INCLUSTER_EVALUATION","value":"false"`, true},
		{"keys never expire", "small-box", ``, `"KEY_IDLE_EXPIRY_DAYS","value":"0"`, false},
		{"shared data", "small-box", `"SAVED_DATA_DEFAULT_KIND":"declare_first"`, `"SAVED_DATA_DEFAULT_KIND","value":"shared"`, true},
		{"looser sign-in rate", "small-box", ``, `"RATE_LIMIT_SIGNIN_IP","value":"40,5s"`, false},
		{"year-long refresh", "small-box", ``, `"OAUTH_REFRESH_TTL_DAYS","value":"365"`, false},
		{"not security-sensitive", "small-box", ``, `"KEEP_VERSIONS","value":"50"`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeSidecar{answer: assistReply("Ok.", `{"changes":[{"setting":`+c.change+`,"why":"x"}]}`)}
			_, mux := newTestSetupAssist(t, f, AskOptions{})
			got := decodeAssist(t, postAssist(mux, `{"product":"`+c.product+`","step":"files","choices":{`+c.choices+`},"message":"q"}`, nil))
			if (len(got.Changes) == 1) != c.kept {
				t.Fatalf("changes %v, kept should be %v", got.Changes, c.kept)
			}
		})
	}
}

// Replies without the marker still work: the text is the answer, and a
// fence or the JSON object itself ends it. A reply that is only JSON gets a
// short answer; one with nothing is a failure.
func TestSetupAssistReplyShapes(t *testing.T) {
	for _, c := range []struct{ reply, answer string }{
		{"Just an answer.", "Just an answer."},
		{"Answer.\n```json\n{\"changes\":[{\"setting\":\"KEEP_VERSIONS\",\"value\":\"5\",\"why\":\"Rollback.\"}]}\n```", "Answer."},
		{"Answer. {\"changes\":[{\"setting\":\"KEEP_VERSIONS\",\"value\":\"5\"}]}", "Answer."},
		{"{\"changes\":[{\"setting\":\"KEEP_VERSIONS\",\"value\":\"5\"}]}", "Here is what I would change."},
		{"Answer.\n" + setupAssistMarker + "\nnot json", "Answer."},
	} {
		f := &fakeSidecar{answer: c.reply}
		_, mux := newTestSetupAssist(t, f, AskOptions{})
		got := decodeAssist(t, postAssist(mux, `{"product":"small-box","step":"files","message":"q"}`, nil))
		if got.Answer != c.answer {
			t.Errorf("reply %q: answer %q, want %q", c.reply, got.Answer, c.answer)
		}
	}
	f := &fakeSidecar{answer: "\n" + setupAssistMarker + "\n{}"}
	_, mux := newTestSetupAssist(t, f, AskOptions{})
	if w := postAssist(mux, `{"product":"small-box","step":"files","message":"q"}`, nil); w.Code != http.StatusBadGateway {
		t.Errorf("empty reply: %d", w.Code)
	}
	f = &fakeSidecar{status: 500}
	_, mux = newTestSetupAssist(t, f, AskOptions{})
	if w := postAssist(mux, `{"product":"small-box","step":"files","message":"q"}`, nil); w.Code != http.StatusBadGateway || askCode(t, w) != "unavailable" {
		t.Errorf("model failure: %d %s", w.Code, w.Body.String())
	}
}

// What the model is sent: the rules, the basic questions, the facts, the
// guide, the troubleshooting text and the settings the helper writes (no
// secret, no setting the helper does not write), then earlier turns and the
// message as JSON with the choices and their defaults. Pasted output,
// the message and earlier questions are redacted again; nothing about the
// visitor goes.
func TestSetupAssistPrompt(t *testing.T) {
	f := &fakeSidecar{answer: assistReply("Ok.", `{"changes":[]}`)}
	_, mux := newTestSetupAssist(t, f, AskOptions{ReasoningEffort: "low"})
	hist := `[{"q":"one","a":"1"},{"q":"two DB_PASSWORD=hunter2","a":"2"},{"q":"three","a":"3"},{"q":"four","a":"4"},{"q":"five","a":"5"}]`
	body := `{"product":"small-box","step":"advanced","mode":"advanced","area":"accounts","choices":{"KEEP_VERSIONS":"1","SIGNIN_CODE_TTL_MINUTES":"5"},"basics":{"codes":"true","google":"false"},"message":"why did it fail? my key is shk_a1b2c3d4e5f6g7h8i9j0","pasted":"load config: RESEND_API_KEY=re_live_abcdefghijkl\nmail to alex@example.com failed","history":` + hist + `}`
	w := postAssist(mux, body, func(r *http.Request) {
		r.Header.Set("User-Agent", "Mozilla/5.0")
		r.Header.Set("Cookie", "sh_session=abc")
	})
	decodeAssist(t, w)
	var up struct {
		Model           string          `json:"model"`
		ReasoningEffort string          `json:"reasoning_effort"`
		MaxTokens       int             `json:"max_tokens"`
		Messages        []openAIMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(f.bodies[0]), &up); err != nil {
		t.Fatal(err)
	}
	if up.Model != "grok-test" || up.ReasoningEffort != "low" || up.MaxTokens != setupAssistMaxTokens {
		t.Fatalf("upstream: model %q effort %q tokens %d", up.Model, up.ReasoningEffort, up.MaxTokens)
	}
	// System, four earlier turns (the last four), the message.
	if len(up.Messages) != 1+8+1 || up.Messages[1].Content != "two DB_PASSWORD=[redacted]" || up.Messages[7].Content != "five" {
		t.Fatalf("messages: %d, %+v", len(up.Messages), up.Messages[1:3])
	}
	sys, user := up.Messages[0].Content, up.Messages[len(up.Messages)-1].Content
	for _, want := range []string{
		setupAssistMarker, "=== BASIC QUESTIONS ===", "- codes: true/false.", "=== FACTS ===", "KEEP_VERSIONS=1 keeps only the live version",
		"=== GUIDE ===", "The installer installs Docker", "=== TROUBLESHOOTING ===", "FAILED: could not fetch",
		"cd /opt/simple-host && sudo docker compose logs", "-- Accounts and sign-in --", "SIGNIN_CODE_TTL_MINUTES | duration | 15 |",
		"KEEP_VERSIONS | int | 0 |", "| security |", "never propose a value weaker", "Pasted output",
	} {
		if !strings.Contains(sys, want) {
			t.Errorf("system prompt lacks %q", want)
		}
	}
	for _, not := range []string{"RESEND_API_KEY |", "ADMIN_API_KEY |", "SITE_DOMAIN |", "WRITE_AUTH_MODE |", "GOOGLE_OAUTH_CLIENT_ID |", "ASK_MODEL |"} {
		if strings.Contains(sys, not) {
			t.Errorf("system prompt lists %q", not)
		}
	}
	var u map[string]any
	if err := json.Unmarshal([]byte(user), &u); err != nil {
		t.Fatalf("user message is not JSON: %v", err)
	}
	if u["product"] != "small-box" || u["message"] != "why did it fail? my key is [redacted key]" {
		t.Errorf("user message: %s", user)
	}
	if u["pasted"] != "load config: RESEND_API_KEY=[redacted]\nmail to [email] failed" {
		t.Errorf("pasted: %q", u["pasted"])
	}
	where, _ := json.Marshal(u["where"])
	if string(where) != `{"area":"Accounts and sign-in","mode":"advanced","step":"advanced"}` {
		t.Errorf("where: %s", where)
	}
	choices, _ := json.Marshal(u["choices"])
	if string(choices) != `[{"default":"0","name":"KEEP_VERSIONS","value":"1"},{"default":"15","name":"SIGNIN_CODE_TTL_MINUTES","value":"5"}]` {
		t.Errorf("choices: %s", choices)
	}
	for _, leak := range []string{"198.51.100", "sh_session", "Mozilla", "hunter2", "re_live", "alex@example.com", "shk_a1b2"} {
		if strings.Contains(f.bodies[0], leak) {
			t.Errorf("upstream request carries %q", leak)
		}
	}
	for k := range f.headers[0] {
		if k != "Authorization" && k != "Content-Type" && k != "Content-Length" && k != "Accept-Encoding" && k != "User-Agent" {
			t.Errorf("upstream request carries header %s", k)
		}
	}
	if ua := f.headers[0].Get("User-Agent"); strings.Contains(ua, "Mozilla") {
		t.Errorf("upstream carries the visitor's User-Agent %q", ua)
	}
	// Enterprise: its own knowledge, and its basic questions.
	f = &fakeSidecar{answer: assistReply("Ok.", `{"changes":[]}`)}
	_, mux = newTestSetupAssist(t, f, AskOptions{})
	decodeAssist(t, postAssist(mux, `{"product":"enterprise","step":"choose","message":"q"}`, nil))
	json.Unmarshal([]byte(f.bodies[0]), &up)
	sys = up.Messages[0].Content
	for _, want := range []string{"entra = Microsoft Entra ID", "MAX_ARCHIVE_BYTES × UPLOAD_CONCURRENCY", "discover OIDC provider", "pgcrypto", "SESSION_TTL | duration | 8h |", "-- Access and teams --"} {
		if !strings.Contains(sys, want) {
			t.Errorf("enterprise prompt lacks %q", want)
		}
	}
	for _, not := range []string{"SESSION_SIGNING_KEY |", "PUBLIC_BASE_URL |", "SECURE_MODE |", "DB_PORT |"} {
		if strings.Contains(sys, not) {
			t.Errorf("enterprise prompt lists %q", not)
		}
	}
	// Both prompts stay a sensible size (about 4 characters a token; each is
	// about 7k today).
	for _, p := range []string{"small-box", "enterprise"} {
		if n := len(setupAssistSystemPrompt(setupRegistryFor(p))) / 4; n > 10000 {
			t.Errorf("%s prompt is about %d tokens", p, n)
		}
	}
}

// The answer streams as text and stops at the marker, even when the marker
// arrives in pieces; the final event carries the cleaned answer and the
// checked changes.
func TestSetupAssistStreams(t *testing.T) {
	f := &streamSidecar{deltas: []string{"Set **SESSION", "_TTL** to 4h for a shorter", " session.\n==", "=CHANGES", "===\n{\"changes\":[{\"setting\":", "\"SESSION_TTL\",\"value\":\"4h\",\"why\":\"Shorter.\"}]}"}}
	srv := f.server(t)
	h := newAskHandler("k", srv.URL+"/v1", "grok-test", testApex, &askMemCounter{}, AskOptions{Burst: 100, Every: time.Second, DailyMax: 10, MaxInFlight: 4, SetupAssistDailyMax: 10})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/setup/assist", h.setupAssist)
	w := postAssist(mux, `{"product":"enterprise","step":"files","message":"stricter sessions"}`, func(r *http.Request) { r.Header.Set("Accept", "text/event-stream") })
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") || w.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream: %d %v", w.Code, w.Header())
	}
	ev := sseEvents(t, w.Body)
	var text strings.Builder
	var final map[string]any
	for _, e := range ev {
		if s, ok := e["t"].(string); ok {
			if strings.Contains(s, "=") || strings.Contains(s, "{") {
				t.Errorf("a piece shows the marker or the JSON: %q", s)
			}
			text.WriteString(s)
		}
		if e["done"] == true {
			final = e
		}
	}
	if strings.TrimSpace(text.String()) != "Set **SESSION_TTL** to 4h for a shorter session." {
		t.Errorf("streamed text %q", text.String())
	}
	if final == nil || final["answer"] != "Set SESSION_TTL to 4h for a shorter session." {
		t.Fatalf("final: %v", final)
	}
	changes, _ := json.Marshal(final["changes"])
	if string(changes) != `[{"setting":"SESSION_TTL","value":"4h","why":"Shorter."}]` {
		t.Errorf("final changes: %s", changes)
	}
}

// Its own daily cap (0 answers none), its own slots and per-network count,
// and the Ask per-address limit it shares with Ask and the check.
func TestSetupAssistCaps(t *testing.T) {
	body := `{"product":"small-box","step":"files","message":"q"}`
	ok := &fakeSidecar{answer: assistReply("Ok.", `{"changes":[]}`)}
	h, mux := newTestSetupAssist(t, ok, AskOptions{SetupAssistDailyMax: 2, SetupCheckDailyMax: 5})
	if cap(h.assistInFlight) != 1 || h.assistNetMax != 40 {
		t.Fatalf("defaults: in flight %d, per network %d", cap(h.assistInFlight), h.assistNetMax)
	}
	for i := 0; i < 2; i++ {
		if w := postAssist(mux, body, nil); w.Code != http.StatusOK {
			t.Fatalf("message %d: %d", i, w.Code)
		}
	}
	if w := postAssist(mux, body, nil); w.Code != http.StatusTooManyRequests || askCode(t, w) != "daily_limit" {
		t.Fatalf("over the daily cap: %d %s", w.Code, w.Body.String())
	}
	if h.checkDaily.(*askMemCounter).count != 0 || h.daily.(*askMemCounter).count != 0 {
		t.Fatal("the assistant used the check's or Ask's daily count")
	}
	// The check still has its own.
	if w := postCheck(mux, `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`, nil); w.Code == http.StatusTooManyRequests {
		t.Fatalf("check after the assistant ran out: %d", w.Code)
	}

	h, mux = newTestSetupAssist(t, ok, AskOptions{SetupAssistDailyMax: -1})
	h.assistDailyMax = 0
	if w := postAssist(mux, body, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("SETUP_ASSIST_DAILY_MAX=0: %d", w.Code)
	}

	// Own slots: a full check or Ask does not stop it; a full assistant is busy.
	h, mux = newTestSetupAssist(t, ok, AskOptions{SetupAssistPerNetworkDaily: 2, MaxInFlight: 1})
	h.inFlight <- struct{}{}
	h.checkInFlight <- struct{}{}
	if w := postAssist(mux, body, nil); w.Code != http.StatusOK {
		t.Fatalf("assistant while Ask and the check are full: %d", w.Code)
	}
	<-h.inFlight
	<-h.checkInFlight
	h.assistInFlight <- struct{}{}
	if w := postAssist(mux, body, nil); w.Code != http.StatusServiceUnavailable || askCode(t, w) != "busy" {
		t.Fatalf("assistant full: %d %s", w.Code, w.Body.String())
	}
	<-h.assistInFlight
	// Per network: one of two used; .9 is the same /24.
	from := func(addr string) func(*http.Request) { return func(r *http.Request) { r.RemoteAddr = addr } }
	if w := postAssist(mux, body, from("198.51.100.9:1")); w.Code != http.StatusOK {
		t.Fatalf("second from the network: %d", w.Code)
	}
	if w := postAssist(mux, body, from("198.51.100.200:1")); w.Code != http.StatusTooManyRequests || askCode(t, w) != "daily_limit" {
		t.Fatalf("third from the network: %d %s", w.Code, w.Body.String())
	}
	if w := postAssist(mux, body, from("203.0.113.5:1")); w.Code != http.StatusOK {
		t.Fatalf("another network: %d", w.Code)
	}

	// The per-address limit is shared with Ask and the check.
	_, limited := newTestSetupAssist(t, ok, AskOptions{Burst: 1, SetupCheckDailyMax: 5})
	if w := postAssist(limited, body, nil); w.Code != http.StatusOK {
		t.Fatalf("first: %d", w.Code)
	}
	if w := postAssist(limited, body, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("per-address limit: %d", w.Code)
	}
	if w := postCheck(limited, `{"product":"small-box","settings":{"KEEP_VERSIONS":"3"}}`, nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the assistant and the check share the per-address limit: %d", w.Code)
	}
}

// Like /v1/ask and the check: no CORS grant, and nothing in the API metrics.
func TestSetupAssistNoCORSNoMetrics(t *testing.T) {
	_, mux := newTestSetupAssist(t, &fakeSidecar{answer: assistReply("Ok.", `{"changes":[]}`)}, AskOptions{})
	body := `{"product":"small-box","step":"files","message":"q"}`
	w := postAssist(CORS(mux), body, nil)
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("assist: %d, ACAO %q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	r := httptest.NewRequest(http.MethodOptions, "/v1/setup/assist", nil)
	r.Header.Set("Origin", "https://evil.example")
	r.Header.Set("Access-Control-Request-Method", "POST")
	pw := httptest.NewRecorder()
	CORS(mux).ServeHTTP(pw, r)
	if pw.Header().Get("Access-Control-Allow-Origin") != "" || pw.Header().Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("preflight was granted: %v", pw.Header())
	}
	m := &APIMetrics{routes: map[routeKey]int64{}, ips: map[string]*ipAgg{}}
	if w := postAssist(m.Wrap(mux), body, nil); w.Code != http.StatusOK {
		t.Fatalf("assist: %d", w.Code)
	}
	if len(m.routes) != 0 || len(m.ips) != 0 {
		t.Fatalf("the assistant was counted: %v", m.routes)
	}
}

// The redaction cases, shared with the page's JavaScript.
func setupRedactCases(t *testing.T) []struct{ Name, In, Out string } {
	t.Helper()
	b, err := os.ReadFile("testdata/setup-redact-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct{ Name, In, Out string }
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 20 {
		t.Fatalf("only %d redaction cases", len(cases))
	}
	return cases
}

func TestSetupRedact(t *testing.T) {
	for _, c := range setupRedactCases(t) {
		if got := setupRedact(c.In); got != c.Out {
			t.Errorf("%s:\n got %q\nwant %q", c.Name, got, c.Out)
		}
		if got := setupRedact(c.Out); got != c.Out {
			t.Errorf("%s: redacting twice changes it: %q", c.Name, got)
		}
	}
}

// The page redacts with the same rules: its <redact> block, run under node,
// gives exactly the Go output for every case.
func TestSetupRedactMatchesPage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, err := staticFiles.ReadFile("static/setup/assist.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(js)
	i, j := strings.Index(src, "// <redact>"), strings.Index(src, "// </redact>")
	if i < 0 || j < i {
		t.Fatal("assist.js lacks the <redact> block")
	}
	cases := setupRedactCases(t)
	var ins []string
	for _, c := range cases {
		ins = append(ins, c.In)
	}
	in, _ := json.Marshal(ins)
	prog := src[i:j] + "\nprocess.stdout.write(JSON.stringify(" + string(in) + ".map(redact)));"
	b, err := exec.Command(node, "-e", prog).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var outs []string
	if err := json.Unmarshal(b, &outs); err != nil {
		t.Fatal(err)
	}
	for k, c := range cases {
		if outs[k] != c.Out {
			t.Errorf("%s: page gives %q, want %q", c.Name, outs[k], c.Out)
		}
	}
}

// The assistant's knowledge files lose no line to their filters (an author
// who writes a forbidden line hears of it here, rather than the model
// silently missing it).
func TestSetupAssistKnowledgeFiltered(t *testing.T) {
	for _, p := range []string{"small-box", "enterprise"} {
		for _, name := range []string{"setup-" + p + ".txt", "setup-troubleshoot-" + p + ".txt"} {
			raw, err := askData.ReadFile("askdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(string(raw), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				forbidden := askLineForbidden(line)
				if strings.HasPrefix(name, "setup-troubleshoot-") {
					forbidden = setupTroubleLineForbidden(line)
				}
				if forbidden {
					t.Errorf("%s: line dropped by its filter: %q", name, line)
				}
			}
		}
	}
}

// The page's basic-question lists and provider ids (setup.js, <setupBasics>)
// are the server's.
func TestSetupBasicsMatchPage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	js, _ := staticFiles.ReadFile("static/setup/setup.js")
	src := string(js)
	i, j := strings.Index(src, "// <setupBasics>"), strings.Index(src, "// </setupBasics>")
	if i < 0 || j < i {
		t.Fatal("setup.js lacks the <setupBasics> block")
	}
	prog := src[i:j] + "\nprocess.stdout.write(JSON.stringify({small: SMALL_BASIC, ent: ENT_BASIC, idps: IDPS.map(function (p) { return p.id; }), buckets: BUCKETS.map(function (p) { return p.id; })}));"
	b, err := exec.Command(node, "-e", prog).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var page struct{ Small, Ent, Idps, Buckets []string }
	if err := json.Unmarshal(b, &page); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(page.Small, setupHelperBasics["small-box"]) || !slices.Equal(page.Ent, setupHelperBasics["enterprise"]) {
		t.Errorf("basic settings: page %v / %v, server %v", page.Small, page.Ent, setupHelperBasics)
	}
	if c := setupBasicChoiceOf("enterprise", "idp"); c == nil || !slices.Equal(page.Idps, c.values) {
		t.Errorf("identity providers: page %v, server %+v", page.Idps, c)
	}
	if c := setupBasicChoiceOf("enterprise", "bucket"); c == nil || !slices.Equal(page.Buckets, c.values) {
		t.Errorf("bucket providers: page %v, server %+v", page.Buckets, c)
	}
}

// The setup page loads the assistant only when it is on; the marker never
// shows, and no other page carries it.
func TestSetupAssistOnlyWhenEnabled(t *testing.T) {
	prev := setupAssistEnabled
	t.Cleanup(func() { setupAssistEnabled = prev })
	for _, on := range []bool{false, true} {
		setupAssistEnabled = on
		body, err := chromePage("setup-helper.html", chromeDataFor(httptest.NewRequest("GET", "/setup", nil), ""))
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		if strings.Contains(s, markerSetupAssist) {
			t.Error("marker left in the page")
		}
		if has := strings.Count(s, `<script src="/setup/assist.js?v=`+setupAssistJSVersion+`"></script>`); has != map[bool]int{false: 0, true: 1}[on] {
			t.Errorf("assistant on=%v: script included %d times", on, has)
		}
	}
	entries, _ := staticFiles.ReadDir("static")
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		b, _ := staticFiles.ReadFile("static/" + e.Name())
		if n := strings.Count(string(b), markerSetupAssist); n != map[bool]int{false: 0, true: 1}[e.Name() == "setup-helper.html"] {
			t.Errorf("%s carries the assistant marker %d times", e.Name(), n)
		}
	}
}

// Enterprise trial F4: an address the answer names is not deleted from a
// command (which left "curl -vkI ."), it becomes the same path on a
// placeholder host. Allowed nowhere else: /v1/ask still drops addresses.
func TestSetupAssistAnswerKeepsCommandsUsable(t *testing.T) {
	reply := assistReply("Confirm with curl -vkI https://plan.alice.e2e.example.net/healthz?x=1. Then check https://dex.e2e.example.net/.well-known/openid-configuration, [the issuer](https://evil.example/x) and [https://evil.example/y](https://evil.example/y) or www.evil.example/z.", `{"changes":[]}`)
	f := &fakeSidecar{answer: reply}
	_, mux := newTestSetupAssist(t, f, AskOptions{})
	got := decodeAssist(t, postAssist(mux, `{"product":"enterprise","step":"files","message":"smoke says not ready"}`, nil))
	want := "Confirm with curl -vkI https://<your-host>/healthz. Then check https://<your-host>/.well-known/openid-configuration, the issuer and https://<your-host>/y or <your-host>/z."
	if got.Answer != want {
		t.Errorf("answer:\n got %q\nwant %q", got.Answer, want)
	}
	if strings.Contains(got.Answer, "e2e.example.net") || strings.Contains(got.Answer, "evil") {
		t.Errorf("a host survived: %q", got.Answer)
	}
	if a := cleanAnswer("See https://evil.example/x now.", nil); a != "See now." {
		t.Errorf("/v1/ask still drops addresses: %q", a)
	}
}

// Enterprise trial F2: a value that is a template (YOUR-ORG, example.com,
// REPLACE_WITH_…, <…>) is never proposed, whatever the setting.
func TestSetupAssistDropsPlaceholderValues(t *testing.T) {
	for _, v := range []string{"https://YOUR-ORG.okta.com", "sites.example.com", "REPLACE_WITH_YOUR_CLIENT_ID", "<your-host>"} {
		if !setupPlaceholderValue.MatchString(v) {
			t.Errorf("%q not taken for a placeholder", v)
		}
	}
	for _, v := range []string{"4h", "12h", "aws:kms", "10,10s", "AES256", "owner"} {
		if setupPlaceholderValue.MatchString(v) {
			t.Errorf("%q taken for a placeholder", v)
		}
	}
}

// The trials' knowledge reaches the model: the UpCloud API token, the price,
// the admin sign-in at /admin, the version command, the internal-CA smoke
// failure, the IdP session advice and the complete config.env; and the rules
// forbid unasked basic answers.
func TestSetupAssistTrialKnowledge(t *testing.T) {
	small := setupAssistPromptFor(setupRegistryFor("small-box"))
	for _, want := range []string{"UPCLOUD_TOKEN", "about $4 a month", "https://<domain>/admin", "docker compose exec -T app simple-host version", "GET /1.3/price", "Connection refused", "There is no need to create an API user when you have a token"} {
		if !strings.Contains(small, want) {
			t.Errorf("small-box prompt lacks %q", want)
		}
	}
	ent := setupAssistPromptFor(setupRegistryFor("enterprise"))
	for _, want := range []string{"CURL_CA_BUNDLE", "exits 60", "match the identity provider's own session policy", "HUMAN STEP D", "make smoke", "PORT, HTTPS_REDIRECT_PORT, OIDC_SCOPES", "never propose a basic answer, or any change, the person did not ask for", "https://<your-host>/<path>"} {
		if !strings.Contains(ent, want) {
			t.Errorf("enterprise prompt lacks %q", want)
		}
	}
}
