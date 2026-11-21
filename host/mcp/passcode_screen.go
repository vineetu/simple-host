package mcp

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const passcodeScreenInfo = "simple-host passcode screen v1"
const passcodeScreenExpired = "This passcode screen expired. Close it and ask again."
const passcodeScreenUnavailable = "The passcode screen cannot open in this chat. Use the dashboard link in the chat."

func (s *Server) passcodeScreenEnabled(c Caller) bool {
	return c.Mode == "" && appFlag(os.Getenv("MCP_APP_PASSCODE"), c.Handle)
}

type passcodeIdentity struct {
	credential [32]byte
	handle     string
}

type passcodeSession struct {
	caller           passcodeIdentity
	site             string
	key              []byte
	created, expires time.Time
}

type passcodeSessionStore struct {
	mu      sync.Mutex
	entries map[string]passcodeSession
	now     func() time.Time
}

func (s *passcodeSessionStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// sweep runs under mu on every store operation.
func (s *passcodeSessionStore) sweep(now time.Time) {
	for id, session := range s.entries {
		if !now.Before(session.expires) {
			delete(s.entries, id)
		}
	}
}

func (s *passcodeSessionStore) put(id string, session passcodeSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	s.sweep(now)
	if s.entries == nil {
		s.entries = map[string]passcodeSession{}
	}
	count, oldestID, globalID := 0, "", ""
	var oldest, global time.Time
	for id, entry := range s.entries {
		if globalID == "" || entry.created.Before(global) {
			globalID, global = id, entry.created
		}
		if entry.caller == session.caller {
			count++
			if oldestID == "" || entry.created.Before(oldest) {
				oldestID, oldest = id, entry.created
			}
		}
	}
	if count >= 5 {
		delete(s.entries, oldestID)
	}
	if len(s.entries) >= 10000 {
		delete(s.entries, globalID)
	}
	session.created = now
	session.expires = now.Add(time.Duration(appLimit("MCP_PASSCODE_SCREEN_TTL_SECONDS", 600)) * time.Second)
	s.entries[id] = session
}

func (s *passcodeSessionStore) take(id string, caller passcodeIdentity, site string) (passcodeSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(s.clock())
	session, ok := s.entries[id]
	delete(s.entries, id) // Consume before checking identity, site, or encrypted input.
	if !ok || session.caller != caller || session.site != site {
		return passcodeSession{}, errors.New(passcodeScreenExpired)
	}
	return session, nil
}

func passcodeAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func passcodeScreenTool() Tool {
	schema := passcodeSchema(true)
	props := schema["properties"].(map[string]any)
	props["session"] = outString("Single-use screen session.")
	props["server_key"] = outString("Ephemeral P-256 public key, base64url.")
	props["sealed"] = outObject(map[string]any{"nonce": outString("AES-GCM nonce, base64url."), "ciphertext": outString("Sealed passcode, base64url.")}, "nonce", "ciphertext")
	return Tool{
		policy: toolPolicy{screenOnly: true, passcodeResult: "screen", analytics: analyticsPolicy{redactAll: true}},
		Name:   "passcode_screen", Title: "Passcode screen",
		Description: "Exchange sealed passcodes with the passcode screen.",
		Meta:        map[string]any{"ui": map[string]any{"visibility": []string{"app"}}},
		InputSchema: object(map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"begin", "show", "set", "generate"}},
			"site":   str(siteDesc), "client_key": str("Ephemeral uncompressed P-256 public key, base64url."),
			"session": str("Single-use screen session."), "nonce": str("AES-GCM nonce, base64url."), "ciphertext": str("Sealed passcode, base64url."),
		}, "action", "site"),
		OutputSchema: schema, Annotations: writes(true, false, true), run: runPasscodeScreen,
	}
}

func runPasscodeScreen(c *call, args map[string]any) (output, error) {
	caller, err := passcodeScreenCaller(c)
	if err != nil {
		return output{}, err
	}
	action, _ := args["action"].(string)
	var session passcodeSession
	if action != "begin" {
		if session, err = takePasscodeSession(c, caller, args); err != nil {
			return output{}, err
		}
	}
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	switch action {
	case "begin":
		return beginPasscodeScreen(c, caller, name, args)
	case "show", "set", "generate":
		return sessionPasscodeScreen(c, session, action, name, args)
	}
	return output{}, errors.New("action must be begin, show, set, or generate")
}

// passcodeScreenCaller is who may use the screen on this call.
func passcodeScreenCaller(c *call) (passcodeIdentity, error) {
	if !c.server.passcodeScreenEnabled(c.caller) || c.wrapped {
		return passcodeIdentity{}, errors.New("passcode_screen is only for the passcode screen")
	}
	// Only a client that declared MCP Apps gets a session. A text-only or
	// not-yet-seen client (after a restart) could be the model itself, running
	// the key exchange and reading the passcode; its tool reply has the link.
	if !c.uiClient {
		return passcodeIdentity{}, errors.New(passcodeScreenUnavailable)
	}
	identity, ok := clientUIKey(c.caller)
	if !ok {
		return passcodeIdentity{}, errors.New(passcodeScreenExpired)
	}
	return passcodeIdentity{credential: identity, handle: c.caller.Handle}, nil
}

// takePasscodeSession consumes the session, even when the site argument is bad.
func takePasscodeSession(c *call, caller passcodeIdentity, args map[string]any) (passcodeSession, error) {
	id, _ := args["session"].(string)
	site, err := siteArg(args)
	if err != nil {
		site, _ = args["site"].(string)
	}
	return c.server.passcodeSessions.take(id, caller, site)
}

// beginPasscodeScreen opens a session after REST confirms the caller's
// current authority over the site's passcode.
func beginPasscodeScreen(c *call, caller passcodeIdentity, name string, args map[string]any) (output, error) {
	out, err := c.passcodeOutput("passcode_screen", name, c.do(http.MethodGet, sitePath(name, "/lock"), nil, nil))
	if err != nil {
		return output{}, err
	}
	clientKey, _ := args["client_key"].(string)
	key, serverKey, err := passcodeKeyExchange(clientKey)
	if err != nil {
		return output{}, err
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return output{}, err
	}
	id := base64.RawURLEncoding.EncodeToString(token)
	c.server.passcodeSessions.put(id, passcodeSession{caller: caller, site: name, key: key})
	out.Structured["session"] = id
	out.Structured["server_key"] = base64.RawURLEncoding.EncodeToString(serverKey)
	out.Text = "Passcode screen ready for " + name + "."
	return out, nil
}

// passcodeKeyExchange derives the session key from the screen's P-256
// public key and returns it with this server's own public key.
func passcodeKeyExchange(clientKey string) (key, serverKey []byte, err error) {
	invalid := errors.New("Invalid passcode screen public key.")
	publicBytes, err := base64.RawURLEncoding.DecodeString(clientKey)
	if err != nil {
		return nil, nil, invalid
	}
	public, err := ecdh.P256().NewPublicKey(publicBytes)
	if err != nil {
		return nil, nil, invalid
	}
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	shared, err := private.ECDH(public)
	if err != nil {
		return nil, nil, invalid
	}
	key, err = hkdf.Key(sha256.New, shared, nil, passcodeScreenInfo, 32)
	if err != nil {
		return nil, nil, err
	}
	return key, private.PublicKey().Bytes(), nil
}

// sessionPasscodeScreen shows, sets or generates the passcode for an open
// session; a passcode travels only sealed with the session key.
func sessionPasscodeScreen(c *call, session passcodeSession, action, name string, args map[string]any) (output, error) {
	aead, err := passcodeAEAD(session.key)
	if err != nil {
		return output{}, err
	}
	id, _ := args["session"].(string)
	res, err := passcodeScreenRequest(c, aead, action, name, id, args)
	if err != nil {
		return output{}, err
	}
	// /lock validation errors describe the rule, never the submitted value.
	// restError reads only error/code (not the passcode in a successful body).
	out, err := c.passcodeOutput("passcode_screen", name, res)
	if err != nil {
		return output{}, err
	}
	if action != "set" {
		return sealPasscode(out, res, aead, name, id)
	}
	if !strings.Contains(out.Text, "nothing changed") {
		out.Text = "Passcode set on " + name + "."
	}
	return out, nil
}

// passcodeScreenRequest makes the /lock call an action stands for.
func passcodeScreenRequest(c *call, aead cipher.AEAD, action, name, id string, args map[string]any) (upstreamResult, error) {
	path := sitePath(name, "/lock")
	switch action {
	case "show":
		return c.do(http.MethodGet, path, nil, nil), nil
	case "generate":
		return c.do(http.MethodPut, path, []byte(`{"generate":true}`), nil), nil
	}
	passcode, err := openSealedPasscode(aead, args, []byte("set|"+name+"|"+id))
	if err != nil {
		return upstreamResult{}, err
	}
	body, _ := json.Marshal(map[string]string{"passcode": string(passcode)})
	return c.do(http.MethodPut, path, body, nil), nil
}

func openSealedPasscode(aead cipher.AEAD, args map[string]any, aad []byte) ([]byte, error) {
	unreadable := errors.New("Could not open the sealed passcode. Try again.")
	nonceText, _ := args["nonce"].(string)
	ciphertextText, _ := args["ciphertext"].(string)
	nonce, err := base64.RawURLEncoding.DecodeString(nonceText)
	if err != nil || len(nonce) != aead.NonceSize() {
		return nil, unreadable
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(ciphertextText)
	if err != nil {
		return nil, unreadable
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, unreadable
	}
	return plaintext, nil
}

// sealPasscode puts the site's readable passcode, sealed, in the answer.
func sealPasscode(out output, res upstreamResult, aead cipher.AEAD, name, id string) (output, error) {
	var lock struct {
		Passcode string `json:"passcode"`
	}
	if err := json.Unmarshal(res.body, &lock); err != nil {
		return output{}, err
	}
	if lock.Passcode == "" || out.Structured["passcode_protected"] != true {
		out.Text = "No readable passcode on " + name + "."
		return out, nil
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return output{}, err
	}
	sealed := aead.Seal(nil, nonce, []byte(lock.Passcode), []byte("show|"+name+"|"+id))
	out.Structured["sealed"] = map[string]any{"nonce": base64.RawURLEncoding.EncodeToString(nonce), "ciphertext": base64.RawURLEncoding.EncodeToString(sealed)}
	out.Text = "Sealed passcode sent to the passcode screen."
	return out, nil
}
