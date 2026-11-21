package servertest

import (
	"sync"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
)

// Named viewers (viewers.go), end to end over the real router. Needs DB_DSN.

type CodeMailer struct {
	mu    sync.Mutex
	codes map[string]string
}

func (m *CodeMailer) SendSignInCode(to, code, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]string{}
	}
	m.codes[to] = code
	return nil
}

func (m *CodeMailer) Code(to string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.codes[to]
}

func (p *PasscodeApp) Grant(t *testing.T, emails ...string) Resp {
	t.Helper()
	return p.At(t, "POST", PcSiteDomain, "/v1/sites/trip/viewers", map[string]any{"emails": emails}, p.Okey)
}

func CookieOf(raw string) map[string]string {
	return map[string]string{"Cookie": accesshttp.VisitorCookieHost + "=" + raw}
}
