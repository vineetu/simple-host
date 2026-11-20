package sites

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateRedirects(t *testing.T) {
	s := &Service{deps: Deps{RedirectsMaxRules: 1}}
	for _, files := range []map[string][]byte{{"_redirects": []byte("/a /a")}, {"_redirects": []byte("/a /b\n/c /d")}} {
		err := s.validateRedirects(files)
		var e *Error
		if !errors.As(err, &e) || e.Code != "redirects_invalid" || !strings.HasPrefix(e.Message, "_redirects: line ") {
			t.Fatal(err)
		}
	}
	if err := s.validateRedirects(map[string][]byte{"nested/_redirects": []byte("invalid")}); err != nil {
		t.Fatal(err)
	}
	if err := s.validateRedirects(map[string][]byte{"_redirects": []byte("/a /b")}); err != nil {
		t.Fatal(err)
	}
}
