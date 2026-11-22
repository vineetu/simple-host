package deploy

import (
	"errors"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites"
)

func TestValidateRedirects(t *testing.T) {
	s := Service{&sites.Service{Deps: sites.Deps{RedirectsMaxRules: 1}}}
	for _, files := range []map[string][]byte{{"_redirects": []byte("/a /a")}, {"_redirects": []byte("/a /b\n/c /d")}} {
		err := s.ValidateRedirects(files)
		var e *sites.Error
		if !errors.As(err, &e) || e.Code != "redirects_invalid" || !strings.HasPrefix(e.Message, "_redirects: line ") {
			t.Fatal(err)
		}
	}
	if err := s.ValidateRedirects(map[string][]byte{"nested/_redirects": []byte("invalid")}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateRedirects(map[string][]byte{"_redirects": []byte("/a /b")}); err != nil {
		t.Fatal(err)
	}
}
