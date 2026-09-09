package eventdns

import "testing"

func TestNormalizeNameCanonicalises(t *testing.T) {
	// DNS is case-insensitive, so capitals are normalised rather than refused.
	got, err := NormalizeName("  Stanford-CS-2026 ")
	if err != nil || got != "stanford-cs-2026" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"stanford-cs-2026", "a", "builds", "x1", "team-rocket-99"} {
		if _, err := NormalizeName(ok); err != nil {
			t.Errorf("rejected valid name %q: %v", ok, err)
		}
	}
	// Each of these would produce a hostname that is invalid, collides with the
	// instance's own names, or is treated specially by a browser or a CA.
	for _, bad := range []string{
		"", "-lead", "trail-", "has space", "under_score",
		"sites", "www", "cname", "api", "admin", "localhost",
		"toooooooooooooooooooooooooooooooooooooooolong",
		"dots.inside",
	} {
		if _, err := NormalizeName(bad); err == nil {
			t.Errorf("accepted invalid name %q", bad)
		}
	}
}

func TestValidatePublicIP(t *testing.T) {
	if err := ValidatePublicIP("85.9.193.170"); err != nil {
		t.Errorf("rejected a public address: %v", err)
	}
	// A private or loopback address would publish a record pointing inside
	// someone's network, and certificate issuance would then fail in a way that
	// looks like our bug rather than their typo.
	for _, bad := range []string{
		"127.0.0.1", "10.0.0.5", "192.168.1.1", "172.16.0.1",
		"169.254.1.1", "224.0.0.1", "0.0.0.0",
		"", "not-an-ip", "2001:db8::1", "85.9.193",
	} {
		if err := ValidatePublicIP(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
