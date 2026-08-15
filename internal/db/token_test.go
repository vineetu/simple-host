package db

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestNewToken(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != tokenBytes*2 {
		t.Fatalf("token length %d, want %d hex chars", len(a), tokenBytes*2)
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Fatalf("token is not hex: %v", err)
	}
	if a == b {
		t.Fatal("two NewToken calls returned the same value")
	}
}

func TestHashTokenLookupShape(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	sum := HashToken(token)
	if len(sum) != 32 {
		t.Fatalf("digest length %d, want 32", len(sum))
	}
	if bytes.Equal(sum, []byte(token)) {
		t.Fatal("digest equals the presented token")
	}
	if hex.EncodeToString(sum) == token {
		t.Fatal("digest hex equals the presented token — stored value would be replayable")
	}
	if bytes.Equal(sum, HashToken(token+"x")) {
		t.Fatal("different inputs hashed to the same digest")
	}
	if !bytes.Equal(sum, HashToken(token)) {
		t.Fatal("HashToken is not deterministic")
	}
	if HashTokenHex(token) != hex.EncodeToString(sum) {
		t.Fatal("HashTokenHex does not match HashToken")
	}
	if strings.Contains(strings.ToLower(HashTokenHex(token)), strings.ToLower(token)) {
		t.Fatal("presented token appears inside its digest hex")
	}
}

func TestRequirePresentedToken(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := requirePresentedToken(tok); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if err := requirePresentedToken("not-hex"); !errors.Is(err, ErrTokenTooShort) {
		t.Fatalf("not-hex: got %v, want ErrTokenTooShort", err)
	}
	if err := requirePresentedToken("aa"); !errors.Is(err, ErrTokenTooShort) {
		t.Fatalf("short hex: got %v, want ErrTokenTooShort", err)
	}
	if err := requirePresentedToken(""); !errors.Is(err, ErrTokenTooShort) {
		t.Fatalf("empty: got %v, want ErrTokenTooShort", err)
	}
}

func TestHashTokenEmptyIsDistinct(t *testing.T) {
	// Empty is rejected by callers before lookup; the digest must still be
	// well-defined and must not collide with a real token.
	empty := HashToken("")
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(empty, HashToken(tok)) {
		t.Fatal("empty input collided with a real token")
	}
}
