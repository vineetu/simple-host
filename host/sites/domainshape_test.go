package sites

import (
	"testing"
)

// One domain shape for site domains and the instance's own domain.
func TestDomainShapeRefusesIP(t *testing.T) {
	if DomainShape("1.2.3.4") == nil || DomainShape("-a.com") == nil || DomainShape("shop.example.com") != nil {
		t.Fatal("domain shape")
	}
}
