package mcp

import (
	"bufio"
	"context"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const frameTestSecret = "fixture-passcode-947281"

func passcodeTestServer(t *testing.T) (*Server, *string, *int) {
	t.Helper()
	t.Setenv("MCP_APP_PASSCODE", "on")
	code, puts := frameTestSecret, 0
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "denied" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":"not allowed"}`))
			return
		}
		if r.URL.Path != "/v1/sites/blog/lock" {
			t.Error("unexpected REST path")
			w.WriteHeader(404)
			return
		}
		if r.Method == "PUT" {
			puts++
			var body struct {
				Passcode string `json:"passcode"`
				Generate bool   `json:"generate"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Generate {
				code = "fixture-generated-638194"
			} else {
				if len(body.Passcode) < 6 {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"a passcode needs at least 6 characters","code":"invalid_passcode"}`))
					return
				}
				code = body.Passcode
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"site": "blog", "passcode_protected": code != "", "passcode": code, "passcode_set_at": "2026-10-30T10:00:00Z"})
	}))
	return s, &code, &puts
}

func screenCaller() Caller {
	return Caller{APIKey: "fixture-key", Handle: "alice", OAuthGrantID: "fixture-grant"}
}

func screenCall(t *testing.T, s *Server, caller Caller, args map[string]any) output {
	t.Helper()
	out, err := runPasscodeScreen(&call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil), caller: caller, uiClient: true}, args)
	if err != nil {
		t.Fatalf("screen action failed: %v", err)
	}
	wire, _ := json.Marshal(out)
	for _, secret := range []string{frameTestSecret, "fixture-generated-638194", "fixture-changed-492810"} {
		if strings.Contains(string(wire), secret) {
			t.Fatal("plaintext leaked in screen result")
		}
	}
	if err := ValidateOutput(passcodeScreenTool().OutputSchema, out.Structured, nil); err != nil {
		t.Fatal(err)
	}
	return out
}

func frameBegin(t *testing.T, s *Server, caller Caller) (string, cipher.AEAD) {
	t.Helper()
	frame, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	out := screenCall(t, s, caller, map[string]any{"action": "begin", "site": "blog", "client_key": base64.RawURLEncoding.EncodeToString(frame.PublicKey().Bytes())})
	raw, err := base64.RawURLEncoding.DecodeString(out.Structured["server_key"].(string))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := frame.ECDH(pub)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hkdf.Key(sha256.New, shared, nil, "simple-host passcode screen v1", 32)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := passcodeAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	return out.Structured["session"].(string), aead
}

func TestPasscodeScreenRoundTrip(t *testing.T) {
	s, code, puts := passcodeTestServer(t)
	caller := screenCaller()
	for _, action := range []string{"show", "generate", "set", "show"} {
		id, frame := frameBegin(t, s, caller)
		args := map[string]any{"action": action, "site": "blog", "session": id}
		if action == "set" {
			nonce := make([]byte, 12)
			_, _ = rand.Read(nonce)
			args["nonce"] = base64.RawURLEncoding.EncodeToString(nonce)
			args["ciphertext"] = base64.RawURLEncoding.EncodeToString(frame.Seal(nil, nonce, []byte("fixture-changed-492810"), []byte("set|blog|"+id)))
		}
		out := screenCall(t, s, caller, args)
		if action == "set" {
			if _, ok := out.Structured["sealed"]; ok {
				t.Fatal("set returned sealed code")
			}
			continue
		}
		sealed := out.Structured["sealed"].(map[string]any)
		nonce, _ := base64.RawURLEncoding.DecodeString(sealed["nonce"].(string))
		ciphertext, _ := base64.RawURLEncoding.DecodeString(sealed["ciphertext"].(string))
		plaintext, err := frame.Open(nil, nonce, ciphertext, []byte("show|blog|"+id))
		if err != nil || string(plaintext) != *code {
			t.Fatal("frame failed to decrypt the code")
		}
		for _, aad := range []string{"set|blog|" + id, "show|other|" + id, "show|blog|other"} {
			if _, err := frame.Open(nil, nonce, ciphertext, []byte(aad)); err == nil {
				t.Fatal("wrong AAD accepted")
			}
		}
		if _, err := runPasscodeScreen(&call{server: s, caller: caller, uiClient: true}, args); err == nil || err.Error() != passcodeScreenExpired {
			t.Fatal("session reused")
		}
	}
	if *puts != 2 || *code != "fixture-changed-492810" {
		t.Fatal("wrong REST writes")
	}
}

func TestPasscodeScreenBindingAndFailures(t *testing.T) {
	s, _, puts := passcodeTestServer(t)
	now := time.Now()
	s.passcodeSessions.now = func() time.Time { return now }
	for _, kind := range []string{"grant", "key", "handle", "site", "expiry", "aad", "nonce", "ciphertext", "validation"} {
		t.Run(kind, func(t *testing.T) {
			caller := screenCaller()
			if kind == "key" {
				caller.OAuthGrantID = ""
			}
			id, frame := frameBegin(t, s, caller)
			args := map[string]any{"action": "show", "site": "blog", "session": id}
			switch kind {
			case "grant":
				caller.OAuthGrantID = "other"
			case "key":
				caller.APIKey = "other"
			case "handle":
				caller.Handle = "other"
			case "site":
				args["site"] = "other"
			case "expiry":
				now = now.Add(601 * time.Second)
			case "aad", "nonce", "ciphertext", "validation":
				args["action"] = "set"
				nonce := make([]byte, 12)
				_, _ = rand.Read(nonce)
				aad, secret := "set|blog|"+id, frameTestSecret
				if kind == "aad" {
					aad = "show|blog|" + id
				}
				if kind == "validation" {
					secret = "short"
				}
				args["nonce"] = base64.RawURLEncoding.EncodeToString(nonce)
				args["ciphertext"] = base64.RawURLEncoding.EncodeToString(frame.Seal(nil, nonce, []byte(secret), []byte(aad)))
				if kind == "nonce" {
					args["nonce"] = "AA"
				}
				if kind == "ciphertext" {
					args["ciphertext"] = "!"
				}
			}
			c := &call{server: s, caller: caller, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
			if _, err := runPasscodeScreen(c, args); err == nil {
				t.Fatal("invalid request accepted")
			}
			args["action"], args["site"] = "show", "blog"
			if _, err := runPasscodeScreen(c, args); err == nil || err.Error() != passcodeScreenExpired {
				t.Fatal("failed attempt did not consume session")
			}
		})
	}
	if *puts != 1 {
		t.Fatal("invalid sealed requests reached REST")
	}
	// Authorization runs before session creation; malformed public keys never create sessions.
	frame, _ := ecdh.P256().GenerateKey(rand.Reader)
	for _, raw := range []string{"!", "AA", base64.RawURLEncoding.EncodeToString(make([]byte, 65)), base64.RawURLEncoding.EncodeToString(frame.PublicKey().Bytes())} {
		caller := screenCaller()
		if raw == base64.RawURLEncoding.EncodeToString(frame.PublicKey().Bytes()) {
			caller.APIKey = "denied"
		}
		if _, err := runPasscodeScreen(&call{server: s, caller: caller, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}, map[string]any{"action": "begin", "site": "blog", "client_key": raw}); err == nil {
			t.Fatal("bad begin accepted")
		}
	}
	if len(s.passcodeSessions.entries) != 0 {
		t.Fatal("failed begin allocated a session")
	}
}

func TestPasscodeSessionCaps(t *testing.T) {
	s, _, _ := passcodeTestServer(t)
	now := time.Now()
	s.passcodeSessions.now = func() time.Time { return now }
	var ids []string
	for range 6 {
		id, _ := frameBegin(t, s, screenCaller())
		ids = append(ids, id)
		now = now.Add(time.Second)
	}
	if len(s.passcodeSessions.entries) != 5 {
		t.Fatal("per-caller cap")
	}
	if _, exists := s.passcodeSessions.entries[ids[0]]; exists {
		t.Fatal("oldest session retained")
	}
	now = now.Add(601 * time.Second)
	frameBegin(t, s, screenCaller())
	if len(s.passcodeSessions.entries) != 1 {
		t.Fatal("expired entries not swept")
	}
	store := &s.passcodeSessions
	store.entries = map[string]passcodeSession{}
	for i := range 10000 {
		store.entries[fmt.Sprint(i)] = passcodeSession{caller: passcodeIdentity{handle: fmt.Sprint(i)}, created: now.Add(time.Duration(i) * time.Nanosecond), expires: now.Add(time.Hour)}
	}
	store.put("new", passcodeSession{caller: passcodeIdentity{handle: "new"}})
	if len(store.entries) != 10000 {
		t.Fatal("global cap")
	}
	if _, ok := store.entries["0"]; ok {
		t.Fatal("global oldest retained")
	}
}

func passcodeWire(t *testing.T, s *Server, caller Caller, name string, args map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(toolCall(name, args)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(WithCaller(req.Context(), caller))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestPasscodeModelResultsNeverContainCode(t *testing.T) {
	for _, flag := range []string{"off", "on"} {
		for _, mode := range []string{"text", "ui", "wrapped", "unknown"} {
			t.Run(flag+"/"+mode, func(t *testing.T) {
				s, _, puts := passcodeTestServer(t)
				t.Setenv("MCP_APP_PASSCODE", flag)
				caller := screenCaller()
				if mode != "unknown" {
					s.clientUI.remember(caller, mode != "text", time.Now())
				}
				for _, action := range []string{"get", "set", "generate", "open"} {
					name := "set_site_passcode"
					args := map[string]any{"site": "blog"}
					switch action {
					case "get":
						name = "get_site_passcode"
					case "set":
						args["passcode"] = frameTestSecret
					case "generate":
						args["generate"] = true
					}
					schemaName := name
					if mode == "wrapped" {
						args = map[string]any{"name": name, "arguments": args}
						name = "run_tool"
					}
					before := *puts
					rec := passcodeWire(t, s, caller, name, args)
					text, data, isErr := resultOf(t, rec)
					if isErr {
						t.Fatal("status call failed")
					}
					for _, secret := range []string{frameTestSecret, "fixture-generated-638194"} {
						if strings.Contains(rec.Body.String(), secret) {
							t.Fatal("plaintext leaked in tool response")
						}
					}
					if err := ValidateOutput(s.byName[schemaName].OutputSchema, data, nil); err != nil {
						t.Fatal(err)
					}
					if action == "open" {
						if *puts != before {
							t.Fatal("opening screen changed passcode")
						}
						want := "https://simple-host.app/alice?passcode=blog"
						if flag == "on" && mode == "ui" {
							want = "Nothing changed yet."
						}
						if flag == "on" && mode == "wrapped" {
							want = "Call get_site_passcode for blog"
						}
						if !strings.Contains(text, want) {
							t.Fatalf("missing expected destination %q", want)
						}
					}
				}
			})
		}
	}
}

func TestPasscodeAppInventory(t *testing.T) {
	s, _, _ := passcodeTestServer(t)
	caller := screenCaller()
	t.Setenv("MCP_APP_PASSCODE", "off")
	off, bareOff, _ := s.forCaller(caller)
	baseline, _ := json.Marshal(off)
	bareBaseline, _ := json.Marshal(bareOff)
	t.Setenv("MCP_APP_PASSCODE", "accounts:alice")
	for _, app := range []string{"Claude", "ChatGPT"} {
		caller.AppName = app
		full, bare, byName := s.forCaller(caller)
		tools := map[string]Tool{}
		for _, tool := range full {
			tools[tool.Name] = tool
		}
		if len(full) != len(bare) {
			t.Fatal("bare listing differs")
		}
		if len(full) != len(off)+2 && app == "Claude" {
			t.Fatal("core did not add two tools")
		}
		for _, name := range []string{"get_site_passcode", "set_site_passcode"} {
			if app == "Claude" && name == "set_site_passcode" {
				continue
			}
			if tools[name].Meta["ui"].(map[string]any)["resourceUri"] != "ui://simple-host/passcode" {
				t.Fatal("missing resource metadata")
			}
			if _, ok := tools[name].OutputSchema["properties"].(map[string]any)["passcode"]; ok {
				t.Fatal("schema exposes passcode")
			}
		}
		ui := tools["passcode_screen"].Meta["ui"].(map[string]any)
		if screenJSON(ui["visibility"]) != `["app"]` || ui["resourceUri"] != nil || byName["passcode_screen"].run == nil {
			t.Fatal("app-only tool metadata")
		}
		for _, query := range []string{"", "passcode_screen", "passcode"} {
			found := s.searchFor(caller).find(query, 10)
			if strings.Contains(screenJSON(found), `"name":"passcode_screen"`) {
				t.Fatal("app tool discovered")
			}
		}
		rec := passcodeWire(t, s, caller, "run_tool", map[string]any{"name": "passcode_screen", "arguments": map[string]any{"site": "blog", "action": "show"}})
		text, _, bad := resultOf(t, rec)
		if !bad || text != "passcode_screen is only for the passcode screen" {
			t.Fatal("wrapper allowed app-only tool")
		}
	}
	if len(s.appResources(caller)) != 1 {
		t.Fatal("missing resource")
	}
	res := s.readApp(request{ID: json.RawMessage(`1`), Params: json.RawMessage(`{"uri":"ui://simple-host/passcode"}`)}, caller)
	content := res.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
	if content["mimeType"] != appMIME || !strings.Contains(content["text"].(string), "Make one for me") || strings.Contains(content["text"].(string), "__SCREEN__") {
		t.Fatal("bad screen resource")
	}
	meta := content["_meta"].(map[string]any)["ui"].(map[string]any)
	for _, domains := range meta["csp"].(map[string][]string) {
		if len(domains) != 0 {
			t.Fatal("network CSP")
		}
	}
	if meta["permissions"].(map[string]any)["clipboardWrite"] == nil {
		t.Fatal("clipboard permission missing")
	}
	for _, c := range []Caller{{Handle: "bob"}, {Handle: "alice", Mode: CallerModeEvents}, {Handle: "alice", Mode: CallerModeHackSite}} {
		_, _, byName := s.forCaller(c)
		if _, ok := byName["passcode_screen"]; ok || len(s.appResources(c)) != 0 {
			t.Fatal("screen gate failed")
		}
	}
	caller.AppName = "Claude"
	t.Setenv("MCP_APP_PASSCODE", "off")
	full, bare, _ := s.forCaller(caller)
	if screenJSON(full) != string(baseline) || screenJSON(bare) != string(bareBaseline) {
		t.Fatal("flag-off cached inventory changed")
	}
	s.fixed = &inventory{tools: off, bare: bareOff, byName: s.byName}
	t.Setenv("MCP_APP_PASSCODE", "on")
	if s.passcodeScreenEnabled(caller) || len(s.appResources(caller)) != 0 {
		t.Fatal("fixed inventory got screen")
	}
}

func screenJSON(value any) string { b, _ := json.Marshal(value); return string(b) }

func TestPasscodeFrameWebCrypto(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	s, _, _ := passcodeTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/passcode-frame.cjs")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard // Assertion values must never become test logs.
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		var args map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &args); err != nil {
			t.Fatal("invalid frame request")
		}
		result, err := runPasscodeScreen(&call{server: s, caller: screenCaller(), orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}, args)
		reply := map[string]any{"result": result.Structured}
		if err != nil {
			reply = map[string]any{"error": err.Error()}
		}
		if err := json.NewEncoder(in).Encode(reply); err != nil {
			t.Fatal("frame pipe closed")
		}
	}
	_ = in.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal("Node frame/WebCrypto check failed")
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestPasscodeScreenNoCodeAndReauthorization(t *testing.T) {
	s, code, _ := passcodeTestServer(t)
	caller := screenCaller()
	id, _ := frameBegin(t, s, caller)
	// OAuth's internal REST credential may rotate while its grant stays the same.
	caller.APIKey = "fixture-rotated"
	screenCall(t, s, caller, map[string]any{"action": "show", "site": "blog", "session": id})
	*code = ""
	id, _ = frameBegin(t, s, caller)
	out := screenCall(t, s, caller, map[string]any{"action": "show", "site": "blog", "session": id})
	if out.Structured["sealed"] != nil || out.Structured["passcode_protected"] != false {
		t.Fatal("unprotected site exposed a code")
	}
	for _, ui := range []bool{false, true} {
		s.clientUI.remember(caller, ui, time.Now())
		text, _, failed := resultOf(t, passcodeWire(t, s, caller, "get_site_passcode", map[string]any{"site": "blog"}))
		if failed || !strings.Contains(text, "has no passcode") {
			t.Fatal("unprotected status missing")
		}
		want := "set one at https://simple-host.app/alice?passcode=blog"
		if ui {
			want = "person can set one there"
		}
		if !strings.Contains(text, want) {
			t.Fatal("unprotected destination missing")
		}
	}
	id, _ = frameBegin(t, s, caller)
	caller.APIKey = "denied"
	c := &call{server: s, caller: caller, orig: httptest.NewRequest("POST", "/mcp", nil), uiClient: true}
	args := map[string]any{"action": "generate", "site": "blog", "session": id}
	if _, err := runPasscodeScreen(c, args); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatal("REST authority not rechecked")
	}
	if _, err := runPasscodeScreen(c, args); err == nil || err.Error() != passcodeScreenExpired {
		t.Fatal("REST refusal did not consume session")
	}
}

func TestPasscodeModernUICapability(t *testing.T) {
	s, _, _ := passcodeTestServer(t)
	for _, ui := range []bool{false, true} {
		caps := map[string]any{}
		if ui {
			caps["extensions"] = map[string]any{"io.modelcontextprotocol/ui": map[string]any{}}
		}
		params := map[string]any{"name": "set_site_passcode", "arguments": map[string]any{"site": "blog"}, "_meta": map[string]any{"io.modelcontextprotocol/clientCapabilities": caps}}
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
		req := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
		req.Header.Set("Mcp-Method", "tools/call")
		req.Header.Set("Mcp-Name", "set_site_passcode")
		req = req.WithContext(WithCaller(req.Context(), screenCaller()))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		text, _, failed := resultOf(t, rec)
		if failed || strings.Contains(rec.Body.String(), frameTestSecret) {
			t.Fatal("modern status result leaked or failed")
		}
		if strings.Contains(text, "The passcode screen is open") != ui {
			t.Fatal("modern UI capability not respected")
		}
	}
}

// A text-only client's model could run the key exchange itself, so it never
// gets a session.
func TestPasscodeScreenRefusesTextOnlyClients(t *testing.T) {
	s, _, _ := passcodeTestServer(t)
	caller := screenCaller()
	s.clientUI.remember(caller, false, time.Now())
	rec := passcodeWire(t, s, caller, "passcode_screen", map[string]any{"action": "begin", "site": "blog", "client_key": "x"})
	text, _, failed := resultOf(t, rec)
	if !failed || !strings.Contains(text, "cannot open in this chat") {
		t.Fatalf("text-only client got a screen session: %s", text)
	}
}

// After a restart the server has not seen the client declare MCP Apps; it
// could be the model itself, so it gets no session either.
func TestPasscodeScreenRefusesUnknownClients(t *testing.T) {
	s, _, _ := passcodeTestServer(t)
	rec := passcodeWire(t, s, screenCaller(), "passcode_screen", map[string]any{"action": "begin", "site": "blog", "client_key": "x"})
	text, _, failed := resultOf(t, rec)
	if !failed || !strings.Contains(text, "cannot open in this chat") {
		t.Fatalf("unknown client got a screen session: %s", text)
	}
}
