package handler

import (
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

// Small-box trial: the admin's first site went to /admin-2/<site>/ because
// "admin" is reserved. The admin row takes the domain's first label instead,
// when it is a usable handle; everyone else keeps their own.
func TestHandleSeed(t *testing.T) {
	admin := &db.User{Username: "admin", IsAdmin: true}
	for domain, want := range map[string]string{
		"spring.example.com":          "spring",
		"hack.example.com":            "organiser", // reserved label
		"E2E-SB-0928.simple-hack.app": "e2e-sb-0928",
		"www.example.com":             "organiser", // reserved label
		"api.example.com":             "organiser",
		"localhost":                   "organiser",
		"":                            "organiser",
		"under_score.example.com":     "organiser", // not a handle as written
	} {
		if got := handleSeed(admin, domain); got != want {
			t.Errorf("admin on %q: %q, want %q", domain, got, want)
		}
	}
	if got := handleSeed(&db.User{Username: "ann@example.com"}, "hack.example.com"); got != "ann@example.com" {
		t.Errorf("a person: %q", got)
	}
	if got := handleSeed(&db.User{Username: "boss@example.com", IsAdmin: true}, "hack.example.com"); got != "boss@example.com" {
		t.Errorf("another admin account: %q", got)
	}
}
