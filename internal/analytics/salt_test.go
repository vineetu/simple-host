package analytics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// Setting ANALYTICS_SALT to hex(sha256(ADMIN_API_KEY + "|visitor")) must keep
// every existing ip_hash identical.
func TestExplicitSaltMatchesDerived(t *testing.T) {
	const key = "example-admin-key"
	sum := sha256.Sum256([]byte(key + "|visitor"))
	explicit := hex.EncodeToString(sum[:])

	derived := NewIngester(nil, "", key, "", "")
	withSalt := NewIngester(nil, "", "some-other-key", "", "").WithSalt(explicit)
	for _, ip := range []string{"203.0.113.7", "2001:db8::1"} {
		if !bytes.Equal(hashIP(derived.salt, ip), hashIP(withSalt.salt, ip)) {
			t.Fatalf("hash for %s differs between derived and explicit salt", ip)
		}
	}
	if NewIngester(nil, "", key, "", "").WithSalt("").salt != derived.salt {
		t.Fatal("empty ANALYTICS_SALT must keep the derived salt")
	}
}
