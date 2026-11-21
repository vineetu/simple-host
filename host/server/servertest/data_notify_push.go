package servertest

import (
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// fakePushKeys returns a plausible (not real-browser) P-256 point and auth
// secret: enough for our encryption wrapper to run its full RFC 8291 path
// against a test push endpoint, without a real browser subscription.
func FakePushKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	_, x, y, err := elliptic.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	point := elliptic.Marshal(elliptic.P256(), x, y)
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(point), base64.RawURLEncoding.EncodeToString(authBytes)
}

func PushSubscribeBody(t *testing.T, endpoint, resource string) map[string]any {
	t.Helper()
	p256dh, auth := FakePushKeys(t)
	// events only matters for an owner subscription (a visitor's request
	// is always overridden to mcp.RecordEventNames regardless); the owner
	// route validates it like the webhook create route does, so tests
	// subscribing as the owner need a valid, non-empty list.
	body := map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": p256dh, "auth": auth}, "label": "test device", "events": append([]string{}, mcp.RecordEventNames...)}
	if resource != "" {
		body["resource"] = resource
	}
	return body
}
