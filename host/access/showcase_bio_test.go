package access

import (
	"testing"
)

func TestNormalizeShowcaseBio(t *testing.T) {
	for _, x := range []struct {
		in, want string
		max      int
		bad      bool
	}{
		{"  My projects\nHello  ", "My projects\nHello", 40, false},
		{"<script>alert('x')</script>", "<script>alert('x')</script>", 40, false},
		{"🌴🌴🌴", "🌴🌴🌴", 3, false},
		{"🌴🌴🌴🌴", "", 3, true},
		{"", "", 3, false},
	} {
		got, err := normalizeBio(x.in, x.max)
		if (err != nil) != x.bad || got != x.want {
			t.Fatalf("normalize bio: %q %v", got, err)
		}
	}
}
