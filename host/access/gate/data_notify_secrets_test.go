package gate

import (
	"bytes"
	"encoding/base64"
	"testing"

	notifyapp "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/sites"
)

func notificationKey(t *testing.T) *Handler {
	t.Helper()
	h := &Handler{Hosts: &sites.Hosts{}}
	if err := h.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	return h
}
func TestPasscodeStoredFormatCompatibility(t *testing.T) {
	h := notificationKey(t)
	p := &h.Passcode
	// Construct the pre-change on-disk format directly, without the shared helper.
	nonce := bytes.Repeat([]byte{1}, 12)
	legacy := p.keys.AEAD.Seal(nonce, nonce, []byte("123456"), []byte("site:site-a"))
	if got, err := p.open("site-a", legacy); err != nil || got != "123456" {
		t.Fatal("old passcode format no longer opens")
	}
	sealed, err := p.Seal("site-a", "123456")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := p.keys.AEAD.Open(nil, sealed[:12], sealed[12:], []byte("site:site-a"))
	if err != nil || string(plain) != "123456" {
		t.Fatal("new passcode format changed")
	}
	if _, err := (notifyapp.Env{AEAD: p.keys.AEAD}).OpenSecret("site-a", "enc1:"+base64.StdEncoding.EncodeToString(sealed)); err == nil {
		t.Fatal("passcode accepted as notification secret")
	}
}
