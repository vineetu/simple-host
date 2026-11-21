package accesshttp

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
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
	if !strings.HasPrefix(encoded, "pbkdf2-sha256$600000$") {
		t.Fatalf("unexpected encoding %q", encoded)
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
	// Two hashes of one password differ (random salt).
	again, _ := HashReviewerPassword(reviewerTestPassword)
	if again == encoded {
		t.Fatal("salt is not random")
	}
	for _, bad := range []string{
		"", "bcrypt$10$x$y", "pbkdf2-sha256$1000$" + strings.Split(encoded, "$")[2] + "$" + strings.Split(encoded, "$")[3],
		"pbkdf2-sha256$600000$!!$" + strings.Split(encoded, "$")[3], "pbkdf2-sha256$600000$" + strings.Split(encoded, "$")[2] + "$short",
		encoded + "$extra",
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
	h := &ConnectorHandler{}
	if h.EnableReviewerSignIn("rev@example.com", "garbage") || h.reviewer != nil {
		t.Fatal("a malformed hash half-enabled reviewer sign-in")
	}
}

func TestReviewerFailureLogsKeyedIP(t *testing.T) {
	encoded, err := HashReviewerPassword(reviewerTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	rv, err := newReviewerSignIn("reviewer@example.com", encoded)
	if err != nil {
		t.Fatal(err)
	}
	h := &ConnectorHandler{reviewer: rv, consentKey: []byte("test process key")}
	var logs bytes.Buffer
	original := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(original) })
	req := httptest.NewRequest("POST", "/oauth/reviewer-sign-in", strings.NewReader(`{"email":"reviewer@example.com","password":"wrong password"}`))
	req.RemoteAddr = "203.0.113.19:1234"
	w := httptest.NewRecorder()
	h.reviewerSignInHandler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", w.Code)
	}
	mac := hmac.New(sha256.New, h.consentKey)
	mac.Write([]byte("reviewer-sign-in:203.0.113.19"))
	want := fmt.Sprintf("ip_hash=%x", mac.Sum(nil)[:6])
	if strings.Contains(logs.String(), "203.0.113.19") || !strings.Contains(logs.String(), want) {
		t.Fatal("failed sign-in did not use the keyed IP hash")
	}
	logs.Reset()
	h.consentKey = []byte("another process key")
	w = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/oauth/reviewer-sign-in", strings.NewReader(`{"email":"reviewer@example.com","password":"wrong password"}`))
	req.RemoteAddr = "203.0.113.19:1234"
	h.reviewerSignInHandler(w, req)
	if w.Code != http.StatusUnauthorized || strings.Contains(logs.String(), want) || !strings.Contains(logs.String(), "ip_hash=") {
		t.Fatal("changing the process key did not change the IP hash")
	}
}
