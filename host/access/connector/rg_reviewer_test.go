package connector

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

func TestReviewerFailureLogsKeyedIP(t *testing.T) {
	encoded, err := HashReviewerPassword(reviewerTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	rv, err := newReviewerSignIn("reviewer@example.com", encoded)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{reviewer: rv, consentKey: []byte("test process key")}
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
