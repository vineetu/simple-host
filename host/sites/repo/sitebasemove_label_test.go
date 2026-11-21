package repo

import "testing"

func TestMovableLabel(t *testing.T) {
	reserved := func(l string) bool { return l == "admin" }
	for _, c := range []struct {
		host  string
		skip  func(string) bool
		label string
		ok    bool
	}{
		{"shop.old.app", nil, "shop", true},
		{"  Shop.OLD.app ", nil, "shop", true},
		{"old.app", nil, "", false},
		{".old.app", nil, "", false},
		{"a.b.old.app", nil, "", false},
		{"www.old.app", nil, "", false},
		{"shop.other.app", nil, "", false},
		{"admin.old.app", reserved, "", false},
		{"shop.old.app", reserved, "shop", true},
	} {
		label, ok := movableLabel(c.host, "old.app", c.skip)
		if label != c.label || ok != c.ok {
			t.Errorf("movableLabel(%q) = %q, %v; want %q, %v", c.host, label, ok, c.label, c.ok)
		}
	}
}
