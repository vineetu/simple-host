// Package email sends transactional mail via Resend.
package email

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	htmlpkg "html"
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
	// codeWords is how long a sign-in code works, in words
	// (SIGNIN_CODE_TTL_MINUTES); empty means the default, "15 minutes".
	codeWords string
	// product names the service in the sign-in email; empty means
	// "Simple Host" (SetProductName).
	product string
}

// SetProductName sets the service name the sign-in email uses ("Simple
// Hack" on the hackathon platform). Call once at startup.
func (s *ResendSender) SetProductName(name string) { s.product = name }

func (s *ResendSender) productName() string {
	if s.product == "" {
		return "Simple Host"
	}
	return s.product
}

// SetCodeLifetime sets the words the sign-in email uses for how long its code
// works ("15 minutes"). Call once at startup.
func (s *ResendSender) SetCodeLifetime(words string) { s.codeWords = words }

func (s *ResendSender) codeLifetime() string {
	if s.codeWords == "" {
		return "15 minutes"
	}
	return s.codeWords
}

func NewResendSender(apiKey, from string) *ResendSender {
	return &ResendSender{
		apiKey: apiKey,
		from:   from,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// SendSignInCode delivers the verification code and an optional sign-in link.
// The code appears in the subject line so the user can read it without opening
// the mail; the link gives them a one-click browser sign-in.
func (s *ResendSender) SendSignInCode(toEmail, code, link string) error {
	if s.apiKey == "" {
		return errors.New("RESEND_API_KEY not configured")
	}
	subject, text, html := s.signInCodeMessage(code, link)
	return s.send(toEmail, subject, text, html)
}

// signInCodeMessage builds the sign-in email's subject and bodies.
func (s *ResendSender) signInCodeMessage(code, link string) (subject, text, html string) {
	textLink, htmlLink := "", ""
	if link != "" {
		textLink = fmt.Sprintf("Or click this link to sign in in your browser:\n%s\n\n", link)
		htmlLink = fmt.Sprintf(`<p style="margin-top: 24px;">Or <a href="%s" style="color: #c96442;">click here to sign in in your browser</a>.</p>`, link)
	}
	subject = fmt.Sprintf("%s sign-in code: %s", s.productName(), code)
	text = fmt.Sprintf(`Your %s sign-in code:

    %s

%sThis code expires in %s. If you didn't request this, you can ignore the email.
`, s.productName(), code, textLink, s.codeLifetime())

	html = fmt.Sprintf(`<!DOCTYPE html>
<html><body style="font-family: -apple-system, system-ui, sans-serif; color: #1a1a1a; max-width: 480px; margin: 0 auto; padding: 24px;">
<h2 style="font-weight: 600; letter-spacing: -0.3px;">%s sign-in</h2>
<p>Your code is:</p>
<div style="font-family: ui-monospace, monospace; font-size: 32px; font-weight: 600; letter-spacing: 4px; padding: 16px 24px; background: #faf9f7; border: 1px solid #e8e5e0; border-radius: 8px; display: inline-block; color: #c96442;">%s</div>
%s
<p style="color: #6b6560; font-size: 13px; margin-top: 32px;">This code expires in %s. If you didn't request this, you can ignore the email.</p>
</body></html>`, htmlpkg.EscapeString(s.productName()), code, htmlLink, s.codeLifetime())
	return subject, text, html
}

// SendNotice delivers a plain-text account notice (e.g. a domain that has
// stopped working). text is shown as-is; the HTML part is the same text,
// escaped.
func (s *ResendSender) SendNotice(toEmail, subject, text string) error {
	if s.apiKey == "" {
		return errors.New("RESEND_API_KEY not configured")
	}
	html := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, system-ui, sans-serif; color: #1a1a1a; max-width: 520px; margin: 0 auto; padding: 24px; white-space: pre-wrap;">` +
		htmlpkg.EscapeString(text) + `</body></html>`
	return s.send(toEmail, subject, text, html)
}

// CanSendNotice reports whether this sender has the credential needed for notices.
func (s *ResendSender) CanSendNotice() bool { return s.apiKey != "" }

// SendNoticeReplyTo is SendNotice with a Reply-To address, for notices the
// person may want to answer (the answer goes to support, not to the sender).
func (s *ResendSender) SendNoticeReplyTo(toEmail, replyTo, subject, text string) error {
	if s.apiKey == "" {
		return errors.New("RESEND_API_KEY not configured")
	}
	html := `<!DOCTYPE html>
<html><body style="font-family: -apple-system, system-ui, sans-serif; color: #1a1a1a; max-width: 520px; margin: 0 auto; padding: 24px; white-space: pre-wrap;">` +
		htmlpkg.EscapeString(text) + `</body></html>`
	return s.sendWith(toEmail, replyTo, subject, text, html)
}

func (s *ResendSender) send(toEmail, subject, text, html string) error {
	return s.sendWith(toEmail, "", subject, text, html)
}

func (s *ResendSender) sendWith(toEmail, replyTo, subject, text, html string) error {
	msg := map[string]any{
		"from":    s.from,
		"to":      []string{toEmail},
		"subject": subject,
		"text":    text,
		"html":    html,
	}
	if replyTo != "" {
		msg["reply_to"] = replyTo
	}
	body, err := json.Marshal(msg)
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
		// Drain so the connection can be reused, but do NOT propagate the
		// upstream body — it can echo request details into our logs. The
		// status code is enough to diagnose Resend misconfig.
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("resend returned status %d", resp.StatusCode)
	}
	return nil
}
