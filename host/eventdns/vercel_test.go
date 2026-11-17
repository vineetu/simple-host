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
	if _, err := ValidatePublicIP("85.9.193.170"); err != nil {
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
		if _, err := ValidatePublicIP(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestValidatePublicIPRejectsNonRoutableRanges(t *testing.T) {
	// Neither private nor routable. A record pointing at one can never work, and
	// refusing it now is a clearer error than a certificate failure later.
	for _, bad := range []string{
		"0.0.0.1",         // "this network"
		"100.64.0.1",      // carrier-grade NAT
		"192.0.0.8",       // IETF protocol assignments
		"192.0.2.10",      // documentation
		"198.18.0.1",      // benchmarking
		"198.51.100.7",    // documentation
		"203.0.113.9",     // documentation
		"240.0.0.1",       // reserved
		"::ffff:10.0.0.5", // IPv4-mapped private
	} {
		if _, err := ValidatePublicIP(bad); err == nil {
			t.Errorf("accepted non-routable %q", bad)
		}
	}
	// Genuinely routable addresses still pass.
	for _, ok := range []string{"85.9.193.170", "8.8.8.8", "1.1.1.1"} {
		if _, err := ValidatePublicIP(ok); err != nil {
			t.Errorf("rejected routable %q: %v", ok, err)
		}
	}
}

func TestValidatePublicIPCanonicalises(t *testing.T) {
	// A mapped form is a valid IPv4 address that a DNS provider will reject as an
	// A record value, so the caller must send back what was validated.
	got, err := ValidatePublicIP("::ffff:8.8.8.8")
	if err != nil || got != "8.8.8.8" {
		t.Errorf("got %q, %v; want 8.8.8.8", got, err)
	}
	if got, _ := ValidatePublicIP("  85.9.193.170 "); got != "85.9.193.170" {
		t.Errorf("whitespace not trimmed: %q", got)
	}
}
