package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/outbound"
)

// Failure is a delivery or validation outcome category ("invalid_url",
// "refused_address", "timeout", ...), stored as a delivery's result.
type Failure = outbound.Failure

// Result is one send's outcome, also the JSON a test send answers with.
type Result struct {
	Delivered bool   `json:"delivered"`
	Status    int    `json:"status"`
	Result    string `json:"result"`
	body      []byte
}

// client sends webhooks to public HTTPS addresses only, through the one
// outbound guard (host/outbound).
type client struct {
	outbound.Dialer
	timeout time.Duration
	roots   *x509.CertPool
}

// defaultTimeout is config.DefaultLimits().NotifyTimeout, for a client
// built without one.
const defaultTimeout = 5000 * time.Millisecond

func (c *client) deadline() time.Duration {
	if c.timeout > 0 {
		return c.timeout
	}
	return defaultTimeout
}

// SecretKey decodes a "whsec_" signing secret of 24 to 64 bytes.
func SecretKey(secret string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(secret, "whsec_")
	key, err := base64.StdEncoding.DecodeString(encoded)
	if !ok || err != nil || len(key) < 24 || len(key) > 64 {
		return nil, errors.New("invalid_secret")
	}
	return key, nil
}

// NewSecret is a fresh 32-byte signing secret.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(b), nil
}

// Signature is the Standard Webhooks v1 signature of body.
func Signature(secret, id, ts string, body []byte) (string, error) {
	key, err := SecretKey(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// validate accepts an https URL without credentials or fragment whose host
// resolves to public addresses only.
func (c *client) validate(ctx context.Context, raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.Opaque != "" {
		return nil, Failure("invalid_url")
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return nil, Failure("invalid_url")
		}
	}
	if _, err = c.Addresses(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

// httpClient fetches through the guard's Dial, over TLS 1.2+, and never follows a
// redirect: a public destination must not send the request elsewhere.
func (c *client) httpClient() (*http.Client, func()) {
	transport := &http.Transport{Proxy: nil, DialContext: c.Dial, TLSClientConfig: &tls.Config{RootCAs: c.roots, MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	return &http.Client{Transport: transport, Timeout: c.deadline(), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport.CloseIdleConnections
}

// Category is the stored result for a send that failed with err.
func Category(err error) string {
	var failure Failure
	if errors.As(err, &failure) {
		return string(failure)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	var ce *tls.CertificateVerificationError
	var re tls.RecordHeaderError
	if errors.As(err, &ce) || errors.As(err, &re) || strings.Contains(strings.ToLower(err.Error()), "tls") {
		return "tls_error"
	}
	return "connection_refused"
}

// statusResult names an HTTP answer; only 2xx is delivered.
func statusResult(status int) Result {
	switch {
	case status >= 200 && status < 300:
		return Result{Delivered: true, Status: status, Result: "ok"}
	case status >= 300 && status < 400:
		return Result{Status: status, Result: "redirect"}
	case status >= 400 && status < 500:
		return Result{Status: status, Result: "http_4xx"}
	}
	return Result{Status: status, Result: "http_5xx"}
}

// signatures are the webhook-signature header value: the current secret's
// signature, then the previous one's while it is still honoured.
func signatures(env Env, n model.Notification, id, ts string, body []byte) (string, error) {
	sig, err := signatureFor(env, n.ID, n.Secret, id, ts, body)
	if err != nil || n.PrevSecret == "" || n.PrevSecretUntil == nil || !time.Now().Before(*n.PrevSecretUntil) {
		return sig, err
	}
	old, err := signatureFor(env, n.ID, n.PrevSecret, id, ts, body)
	return sig + " " + old, err
}

func signatureFor(env Env, subscription, stored, id, ts string, body []byte) (string, error) {
	secret, err := env.OpenSecret(subscription, stored)
	if err != nil {
		return "", err
	}
	return Signature(secret, id, ts, body)
}

// request is the signed POST of body to n's URL.
func request(ctx context.Context, env Env, n model.Notification, id string, body []byte) (*http.Request, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig, err := signatures(env, n, id, ts, body)
	if err != nil {
		return nil, Failure("secret_unreadable")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(body))
	if err != nil {
		return nil, Failure("connection_refused")
	}
	if n.Source == "owner" && n.AuthHeaderName != "" {
		header, err := env.OpenSecret(n.ID+":header", n.AuthHeaderValue)
		if err != nil {
			return nil, Failure("secret_unreadable")
		}
		req.Header.Set(n.AuthHeaderName, header)
	}
	headers := map[string]string{"Content-Type": "application/json", "User-Agent": "SimpleHost-Webhooks/1", "webhook-id": id, "webhook-timestamp": ts, "webhook-signature": sig, "X-MCP-Subscription-Id": n.ID}
	if n.Source == "owner" {
		headers["svix-id"], headers["svix-timestamp"], headers["svix-signature"] = id, ts, sig
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// send POSTs one signed webhook. A verification challenge also reads the
// answer's first 4 KiB, which must echo the challenge.
func (c *client) send(ctx context.Context, env Env, n model.Notification, id string, body []byte) Result {
	ctx, cancel := context.WithTimeout(ctx, c.deadline())
	defer cancel()
	if _, err := c.validate(ctx, n.URL); err != nil {
		return Result{Result: Category(err)}
	}
	req, err := request(ctx, env, n, id, body)
	if err != nil {
		return Result{Result: Category(err)}
	}
	httpClient, closeIdle := c.httpClient()
	defer closeIdle()
	res, err := httpClient.Do(req)
	if err != nil {
		return Result{Result: Category(err)}
	}
	defer res.Body.Close()
	result := statusResult(res.StatusCode)
	if strings.HasPrefix(id, "msg_verification_") {
		if result.body, err = io.ReadAll(io.LimitReader(res.Body, 4096)); err != nil {
			return Result{Status: res.StatusCode, Result: Category(err)}
		}
	}
	return result
}
