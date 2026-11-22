package connector

import (
	"strings"
	"testing"
)

const reviewerTestPassword = "correct horse battery staple"

func TestReviewerPasswordHash(t *testing.T) {
	if _, err := HashReviewerPassword("too-short"); err == nil {
		t.Fatal("a short password was hashed")
	}
	encoded, err := HashReviewerPassword(reviewerTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	h, err := parseReviewerHash(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !h.matches(reviewerTestPassword) {
		t.Fatal("the right password did not match")
	}
	for _, wrong := range []string{"", "correct horse battery stapl", reviewerTestPassword + " ", strings.ToUpper(reviewerTestPassword)} {
		if h.matches(wrong) {
			t.Fatalf("%q matched", wrong)
		}
	}
	for _, bad := range []string{
		"", "bcrypt$10$x$y", "pbkdf2-sha256$1000$" + strings.Split(encoded, "$")[2] + "$" + strings.Split(encoded, "$")[3],
	} {
		if _, err := parseReviewerHash(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestReviewerSignInConfig(t *testing.T) {
	encoded, _ := HashReviewerPassword(reviewerTestPassword)
	if rv, err := newReviewerSignIn("", ""); rv != nil || err != nil {
		t.Fatal("unset should be off without an error")
	}
	for _, c := range [][2]string{{"rev@example.com", ""}, {"", encoded}, {"not-an-email", encoded}, {"rev@example.com", "garbage"}} {
		if rv, err := newReviewerSignIn(c[0], c[1]); rv != nil || err == nil {
			t.Errorf("%q/%q: enabled or no error", c[0], c[1])
		}
	}
	rv, err := newReviewerSignIn("  Rev@Example.com ", encoded)
	if err != nil || rv == nil || rv.email != "rev@example.com" {
		t.Fatalf("valid config refused: %v", err)
	}
	h := &Handler{}
	if h.EnableReviewerSignIn("rev@example.com", "garbage") || h.reviewer != nil {
		t.Fatal("a malformed hash half-enabled reviewer sign-in")
	}
}
