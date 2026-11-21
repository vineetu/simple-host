package servertest

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
)

// mailbox captures sign-in codes and notices.
type Mailbox struct {
	Mu      sync.Mutex
	Codes   map[string]string // address -> last sign-in code
	Notices []string          // "to|subject|text"
}

func (m *Mailbox) SendSignInCode(to, code, _ string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if m.Codes == nil {
		m.Codes = map[string]string{}
	}
	m.Codes[to] = code
	return nil
}

func (m *Mailbox) SendNotice(to, subject, text string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.Notices = append(m.Notices, to+"|"+subject+"|"+text)
	return nil
}

// to returns the notices sent to address.
func (m *Mailbox) To(address string) []string {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	var out []string
	for _, n := range m.Notices {
		if strings.HasPrefix(n, address+"|") {
			out = append(out, n)
		}
	}
	return out
}

func NewAccountApp(t *testing.T, eds ...*edition.Edition) (*PrivateApp, *Mailbox) {
	t.Helper()
	mb := &Mailbox{}
	a := NewPrivateAppMailer(t, mb, eds...)
	a.Users.Alerts.Run = func(f func()) { f() }
	// These tests sign the same address in several times.
	a.Users.EmailLimiter = httpx.NewRateLimiter(1000, 1000)
	return a, mb
}

var confirmCodeRe = regexp.MustCompile(`confirmation code: (\d{6})`)

var currentCodeRe = regexp.MustCompile(`together with the one sent to the new address:\s+(\d{6})`)

// requestEmailChange asks to move p to newAddr and returns the code sent to
// the new address and the one sent to the current address.
func RequestEmailChange(t *testing.T, a *PrivateApp, mb *Mailbox, key map[string]string, current, newAddr string) (newCode, curCode string) {
	t.Helper()
	r := a.At(t, "POST", "simple-host.test", "/v1/me/email", map[string]string{"email": newAddr}, key)
	if r.Status != 202 || r.JSON(t)["current_email"] != current {
		t.Fatalf("request change: %d %s", r.Status, r.Body)
	}
	got := mb.To(strings.ToLower(newAddr))
	m := confirmCodeRe.FindStringSubmatch(got[len(got)-1])
	if m == nil {
		t.Fatalf("no code for the new address: %v", got)
	}
	cur := mb.To(current)
	c := currentCodeRe.FindStringSubmatch(cur[len(cur)-1])
	if c == nil {
		t.Fatalf("no code for the current address: %v", cur)
	}
	return m[1], c[1]
}

// signIn runs the emailed-code sign-in with the given browser.
func SignInWith(t *testing.T, a *PrivateApp, mb *Mailbox, address, ua string) {
	t.Helper()
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": address}, map[string]string{"User-Agent": ua}); r.Status != 202 {
		t.Fatalf("sign-in request: %d %s", r.Status, r.Body)
	}
	mb.Mu.Lock()
	code := mb.Codes[address]
	mb.Mu.Unlock()
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"email": address, "code": code}, map[string]string{"User-Agent": ua}); r.Status != 200 {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
}

const (
	UaMacChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	UaIPhone    = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
)
