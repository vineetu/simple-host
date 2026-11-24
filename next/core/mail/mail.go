// Package mail sends the service's own emails through Resend (copied from the
// old host/email/resend.go and trimmed to the one send).
package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
)

// Mail sends through a Resend-compatible endpoint. URL is
// https://api.resend.com/emails in production and the fake in tests.
type Mail struct {
	URL, Key, From string
	Client         *http.Client
}

// Send delivers a plain-text email. The HTML part is the same text, escaped.
// The upstream answer body is never logged or returned: it can echo the
// message.
func (m Mail) Send(ctx context.Context, to, subject, text string) error {
	if m.Key == "" || m.URL == "" {
		return errors.New("mail is not configured (set RESEND_API_KEY)")
	}
	body, err := json.Marshal(map[string]any{
		"from": m.From, "to": []string{to}, "subject": subject, "text": text,
		"html": `<!DOCTYPE html><html><body style="font-family: -apple-system, system-ui, sans-serif; color: #1a1a1a; max-width: 520px; margin: 0 auto; padding: 24px; white-space: pre-wrap;">` +
			html.EscapeString(text) + `</body></html>`,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.Key)
	req.Header.Set("Content-Type", "application/json")
	res, err := m.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 300 {
		return fmt.Errorf("resend returned status %d", res.StatusCode)
	}
	return nil
}
