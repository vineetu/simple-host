// Package email sends transactional mail via Resend.
package email

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const resendEndpoint = "https://api.resend.com/emails"

// Sender abstracts the email backend so tests / local dev can swap it out.
type Sender interface {
	SendSignInCode(toEmail, code, link string) error
}

// ResendSender posts to the Resend HTTP API.
type ResendSender struct {
	apiKey string
	from   string
	client *http.Client
}

func NewResendSender(apiKey, from string) *ResendSender {
	return &ResendSender{
		apiKey: apiKey,
		from:   from,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// SendSignInCode delivers the verification code and a sign-in link in one email.
// The code appears in the subject line so the user can read it without opening
// the mail; the link gives them a one-click browser sign-in.
func (s *ResendSender) SendSignInCode(toEmail, code, link string) error {
	if s.apiKey == "" {
		return errors.New("RESEND_API_KEY not configured")
	}

	subject := fmt.Sprintf("Simple Host sign-in code: %s", code)
	text := fmt.Sprintf(`Your Simple Host sign-in code:

    %s

Or click this link to sign in in your browser:
%s

This code expires in 15 minutes. If you didn't request this, you can ignore the email.
`, code, link)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html><body style="font-family: -apple-system, system-ui, sans-serif; color: #1a1a1a; max-width: 480px; margin: 0 auto; padding: 24px;">
<h2 style="font-weight: 600; letter-spacing: -0.3px;">Simple Host sign-in</h2>
<p>Your code is:</p>
<div style="font-family: ui-monospace, monospace; font-size: 32px; font-weight: 600; letter-spacing: 4px; padding: 16px 24px; background: #faf9f7; border: 1px solid #e8e5e0; border-radius: 8px; display: inline-block; color: #c96442;">%s</div>
<p style="margin-top: 24px;">Or <a href="%s" style="color: #c96442;">click here to sign in in your browser</a>.</p>
<p style="color: #6b6560; font-size: 13px; margin-top: 32px;">This code expires in 15 minutes. If you didn't request this, you can ignore the email.</p>
</body></html>`, code, link)

	body, err := json.Marshal(map[string]any{
		"from":    s.from,
		"to":      []string{toEmail},
		"subject": subject,
		"text":    text,
		"html":    html,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", resendEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		buf, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("resend %d: %s", resp.StatusCode, string(buf))
	}
	return nil
}
