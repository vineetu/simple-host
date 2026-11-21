package access

import (
	"strings"
	"testing"
)

func TestPKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" // RFC 7636 appendix B
	if !pkceS256(verifier, challenge) {
		t.Fatal("RFC 7636 example did not verify")
	}
	if pkceS256(verifier+"x", challenge) || pkceS256(challenge, challenge) {
		t.Fatal("wrong verifier accepted")
	}
	if ValidPKCEValue("short") || ValidPKCEValue(strings.Repeat("a", 129)) || ValidPKCEValue(strings.Repeat("a", 42)+"!") {
		t.Fatal("malformed PKCE value accepted")
	}
}

func TestMaskEmail(t *testing.T) {
	if got := maskEmail("new.person@example.com"); got != "n***@example.com" {
		t.Errorf("maskEmail = %q", got)
	}
}
