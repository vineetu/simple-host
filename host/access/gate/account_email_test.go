package gate

import (
	"sync"
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
