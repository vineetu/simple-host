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
	"net/url"
	"os"
	"sync"
	"time"
)

const passcodeScreenInfo = "simple-host passcode screen v1"
const passcodeScreenExpired = "This passcode screen expired. Close it and ask again."

func (s *Server) passcodeScreenEnabled(c Caller) bool {
	return s.fixed == nil && c.Mode == "" && appFlag(os.Getenv("MCP_APP_PASSCODE"), c.Handle)
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
		Name: "passcode_screen", Title: "Passcode screen",
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
	if !c.server.passcodeScreenEnabled(c.caller) || c.wrapped {
		return output{}, errors.New("passcode_screen is only for the passcode screen")
	}
	identity, ok := clientUIKey(c.caller)
	if !ok {
		return output{}, errors.New(passcodeScreenExpired)
	}
	caller := passcodeIdentity{credential: identity, handle: c.caller.Handle}
	action, _ := args["action"].(string)
	name, _ := args["site"].(string)
	id, _ := args["session"].(string)
	var session passcodeSession
	if action != "begin" {
		var err error
		session, err = c.server.passcodeSessions.take(id, caller, name)
		if err != nil {
			return output{}, err
		}
	}
	name, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	path := "/v1/sites/" + url.PathEscape(name) + "/lock"
	switch action {
	case "begin":
		// REST checks the caller's current authority before issuing any session.
		res := c.do(http.MethodGet, path, nil, nil)
		out, err := passcodeOutput("passcode_screen", name, res)
		if err != nil {
			return output{}, err
		}
		raw, _ := args["client_key"].(string)
		publicBytes, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return output{}, errors.New("Invalid passcode screen public key.")
		}
		public, err := ecdh.P256().NewPublicKey(publicBytes)
		if err != nil {
			return output{}, errors.New("Invalid passcode screen public key.")
		}
		private, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return output{}, err
		}
		shared, err := private.ECDH(public)
		if err != nil {
			return output{}, errors.New("Invalid passcode screen public key.")
		}
		key, err := hkdf.Key(sha256.New, shared, nil, passcodeScreenInfo, 32)
		if err != nil {
			return output{}, err
		}
		token := make([]byte, 32)
		if _, err := rand.Read(token); err != nil {
			return output{}, err
		}
		id = base64.RawURLEncoding.EncodeToString(token)
		c.server.passcodeSessions.put(id, passcodeSession{caller: caller, site: name, key: key})
		out.Structured["session"] = id
		out.Structured["server_key"] = base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes())
		out.Text = "Passcode screen ready for " + name + "."
		return out, nil
	case "show", "set", "generate":
		aead, err := passcodeAEAD(session.key)
		if err != nil {
			return output{}, err
		}
		var res upstreamResult
		switch action {
		case "show":
			res = c.do(http.MethodGet, path, nil, nil)
		case "generate":
			res = c.do(http.MethodPut, path, []byte(`{"generate":true}`), nil)
		case "set":
			nonceText, _ := args["nonce"].(string)
			ciphertextText, _ := args["ciphertext"].(string)
			nonce, err := base64.RawURLEncoding.DecodeString(nonceText)
			if err != nil || len(nonce) != aead.NonceSize() {
				return output{}, errors.New("Could not open the sealed passcode. Try again.")
			}
			ciphertext, err := base64.RawURLEncoding.DecodeString(ciphertextText)
			if err != nil {
				return output{}, errors.New("Could not open the sealed passcode. Try again.")
			}
			plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("set|"+name+"|"+id))
			if err != nil {
				return output{}, errors.New("Could not open the sealed passcode. Try again.")
			}
			body, _ := json.Marshal(map[string]string{"passcode": string(plaintext)})
			res = c.do(http.MethodPut, path, body, nil)
		}
		// /lock validation errors describe the rule, never the submitted value.
		// restError reads only error/code (not the passcode in a successful body).
		out, err := passcodeOutput("passcode_screen", name, res)
		if err != nil {
			return output{}, err
		}
		out.Text = "Passcode set on " + name + "."
		if action != "set" {
			var lock struct {
				Passcode string `json:"passcode"`
			}
			if err := json.Unmarshal(res.body, &lock); err != nil {
				return output{}, err
			}
			if lock.Passcode != "" && out.Structured["passcode_protected"] == true {
				nonce := make([]byte, aead.NonceSize())
				if _, err := rand.Read(nonce); err != nil {
					return output{}, err
				}
				sealed := aead.Seal(nil, nonce, []byte(lock.Passcode), []byte("show|"+name+"|"+id))
				out.Structured["sealed"] = map[string]any{"nonce": base64.RawURLEncoding.EncodeToString(nonce), "ciphertext": base64.RawURLEncoding.EncodeToString(sealed)}
				out.Text = "Sealed passcode sent to the passcode screen."
			} else {
				out.Text = "No readable passcode on " + name + "."
			}
		}
		return out, nil
	default:
		return output{}, errors.New("action must be begin, show, set, or generate")
	}
}
