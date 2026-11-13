package hackhttp

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	httpx "github.com/vsriram/simple-host/internal/httpx"
)

const opaqueOwner = "1b4e28ba-2fa1-11d2-883f-0016d3cca427"
const opaqueSite = "2b4e28ba-2fa1-11d2-883f-0016d3cca427"
const opaqueTeam = "3b4e28ba-2fa1-11d2-883f-0016d3cca427"

func assertOpaqueToken(t *testing.T, token string) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{opaqueOwner, opaqueSite, opaqueTeam} {
		uuidBytes, _ := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
		for _, representation := range []string{id, strings.ReplaceAll(id, "-", ""), hex.EncodeToString([]byte(id)), base64.RawURLEncoding.EncodeToString([]byte(id)), base64.StdEncoding.EncodeToString([]byte(id)), base64.RawURLEncoding.EncodeToString(uuidBytes)} {
			if strings.Contains(token, representation) || bytes.Contains(raw, []byte(representation)) {
				t.Fatal("token reveals an internal identifier")
			}
		}
		if bytes.Contains(raw, uuidBytes) {
			t.Fatal("decoded token reveals UUID bytes")
		}
	}
}
func TestOpaqueLinkPayloadsAndAuthentication(t *testing.T) {
	key := func() []byte {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return b
	}()
	hack := NewHackHandlerForTest(key, config.DefaultLimits())
	now := time.Now().Truncate(time.Second)
	expiry := now.Add(time.Minute)
	claim := HackArchiveClaim{UserID: opaqueOwner, EventID: opaqueSite, TeamID: opaqueTeam, Slug: "event", Kind: "own-team", Expiry: expiry.Unix()}
	cases := []struct {
		name, domain string
		mint         func() string
	}{
		{"hack", HackArchiveLinkDomain, func() string { return hack.SignHackArchiveClaim(claim) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := tc.mint()
			assertOpaqueToken(t, token)
			if token == tc.mint() {
				t.Error("nonce reused")
			}
			plain, err := httpx.OpenLink(key, tc.domain, token)
			if err != nil || !bytes.Contains(plain, []byte(opaqueSite)) {
				t.Fatal("payload failed to round trip")
			}
			raw, _ := base64.RawURLEncoding.DecodeString(token)
			raw[len(raw)/2] ^= 1
			for _, bad := range []string{"", token[:len(token)/2], base64.RawURLEncoding.EncodeToString(raw)} {
				if _, err := httpx.OpenLink(key, tc.domain, bad); err == nil {
					t.Error("tampered token accepted")
				}
			}
			if _, err := httpx.OpenLink(func() []byte {
				b := make([]byte, 32)
				if _, err := rand.Read(b); err != nil {
					t.Fatal(err)
				}
				return b
			}(), tc.domain, token); err == nil {
				t.Error("wrong key accepted")
			}
			if _, err := httpx.OpenLink(key, tc.domain+"other", token); err == nil {
				t.Error("wrong link kind accepted")
			}
		})
	}
	if got, ok := hack.CheckHackArchiveClaim(cases[0].mint(), now); !ok || got != claim {
		t.Fatal("archive binding lost")
	}
	for _, exp := range []time.Time{now.Add(-time.Second), now.Add(10*time.Minute + time.Minute)} {
		bad := claim
		bad.Expiry = exp.Unix()
		if _, ok := hack.CheckHackArchiveClaim(hack.SignHackArchiveClaim(bad), now); ok {
			t.Error("archive expiry accepted")
		}
	}
}
