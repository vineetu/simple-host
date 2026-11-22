package signin

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"sync"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"
)

// mailbox captures sign-in codes and notices.
type mailbox struct {
	mu      sync.Mutex
	codes   map[string]string // address -> last sign-in code
	notices []string          // "to|subject|text"
}

func (m *mailbox) SendSignInCode(to, code, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]string{}
	}
	m.codes[to] = code
	return nil
}

func (m *mailbox) SendNotice(to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notices = append(m.notices, to+"|"+subject+"|"+text)
	return nil
}

// geoBlockFor builds a accounts.SignupGeoBlock whose lookup maps ip to iso.
func geoBlockFor(t *testing.T, ip, iso string, blocked []string) accounts.SignupGeoBlock {
	t.Helper()
	return accounts.NewSignupGeoBlock(countryLookup{ip: iso}, blocked)
}

func testNonceHash(n string) sql.NullString {
	sum := sha256.Sum256([]byte(n))
	return sql.NullString{String: base64.RawURLEncoding.EncodeToString(sum[:]), Valid: true}
}

// countryLookup is a local country lookup fake.
type countryLookup map[string]string

func (g countryLookup) CountryISO(ip string) string { return g[ip] }
