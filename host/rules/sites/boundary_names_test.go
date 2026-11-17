package sites

import (
	"testing"
)

func TestValidateSiteShapeAcceptsExistingNames(t *testing.T) {
	// A representative sample of names actually deployed on the box, plus the
	// longest one. None may regress to "invalid".
	existing := []string{
		"jot", "jot-code", "jot-transcribe-app", "padkit", "padkit-tv", "ori",
		"ori-app", "ori-letter-flow", "bass", "paragliding-beginners-map",
		"nyc-puerto-rico-2026", "acme-health-demo", "kulkarni-is-awesome",
		"xianqu-box-sample-20260626", "open-kit", "auto-tune", "mixologist",
	}
	for _, name := range existing {
		if err := ValidateShape(name); err != nil {
			t.Errorf("existing site %q rejected by ValidateShape: %v", name, err)
		}
	}
}

func TestValidateSiteShapeRejectsAbuse(t *testing.T) {
	bad := []string{
		"",                    // empty
		"-leading",            // leading hyphen
		"trailing-",           // trailing hyphen
		"UPPER",               // uppercase
		"under_score",         // underscore
		"dot.name",            // dot
		"has space",           // space
		"evil/../escape",      // slash / traversal
		"inject\nsecond-line", // newline (deploy-queue injection)
		"inject\r",            // carriage return
		"name;rm -rf",         // shell metacharacters
		"`backtick`",          // command substitution
		"$(whoami)",           // command substitution
		strings64(),           // 64 chars — exceeds DNS label limit
	}
	for _, name := range bad {
		if err := ValidateShape(name); err == nil {
			t.Errorf("abusive name %q was accepted by ValidateShape", name)
		}
	}
}

func TestValidateSiteReservedCreateOnly(t *testing.T) {
	for _, name := range []string{"simple-host", "jot-webhook", "api", "admin", "www"} {
		if err := ValidateReserved(name); err == nil {
			t.Errorf("reserved name %q should be rejected on create", name)
		}
	}
	// jot is a live site — it must NOT be reserved, or its redeploys break.
	if err := ValidateReserved("jot"); err != nil {
		t.Errorf("jot must not be reserved (it is a live site): %v", err)
	}
}

func strings64() string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

func TestReservedNewSiteNames(t *testing.T) {
	for _, n := range []string{"support", "admin", "login", "simplehack"} {
		if ValidateReserved(n) == nil {
			t.Errorf("site %q allowed", n)
		}
	}
	for _, n := range []string{"blog", "docs", "team", "test", "my-shop"} {
		if err := ValidateReserved(n); err != nil {
			t.Errorf("site %q refused: %v", n, err)
		}
	}
}
