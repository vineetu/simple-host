package mcp

import (
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"
)

// Initialize-era clients declare MCP Apps support once, in initialize, and
// send no capabilities on later calls. The server keeps no sessions, so it
// remembers the answer per connection (the OAuth grant, else the API key)
// to decide whether create_upload_link opens the picker or returns a link.
type clientUIMemory struct {
	mu      sync.Mutex
	entries map[[32]byte]clientUIEntry
}

type clientUIEntry struct {
	ui   bool
	seen time.Time
}

const (
	clientUIMaxEntries = 20000
	clientUITTL        = 30 * 24 * time.Hour
)

func clientUIKey(c Caller) ([32]byte, bool) {
	switch {
	case c.OAuthGrantID != "":
		return sha256.Sum256([]byte("grant:" + c.OAuthGrantID)), true
	case c.APIKey != "":
		return sha256.Sum256([]byte("key:" + c.APIKey)), true
	}
	return [32]byte{}, false
}

// initializeDeclaresUI reads an initialize request's capabilities.
func initializeDeclaresUI(params json.RawMessage) bool {
	var p struct {
		Capabilities struct {
			Extensions map[string]json.RawMessage `json:"extensions"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(params, &p) != nil {
		return false
	}
	_, ui := p.Capabilities.Extensions["io.modelcontextprotocol/ui"]
	return ui
}

func (m *clientUIMemory) remember(c Caller, ui bool, now time.Time) {
	key, ok := clientUIKey(c)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[[32]byte]clientUIEntry)
	}
	if _, present := m.entries[key]; !present && len(m.entries) >= clientUIMaxEntries {
		oldestKey, oldest := key, now
		for k, e := range m.entries {
			if now.Sub(e.seen) > clientUITTL {
				delete(m.entries, k)
			} else if e.seen.Before(oldest) {
				oldestKey, oldest = k, e.seen
			}
		}
		if len(m.entries) >= clientUIMaxEntries {
			delete(m.entries, oldestKey)
		}
	}
	m.entries[key] = clientUIEntry{ui: ui, seen: now}
}

// lookup reports what the connection last declared; known is false when it
// has not initialized since this process started.
func (m *clientUIMemory) lookup(c Caller, now time.Time) (ui, known bool) {
	key, ok := clientUIKey(c)
	if !ok {
		return false, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, present := m.entries[key]
	if !present || now.Sub(e.seen) > clientUITTL {
		return false, false
	}
	return e.ui, true
}
