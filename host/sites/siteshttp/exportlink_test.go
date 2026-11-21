package siteshttp

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
)

func TestExportTokenRefusesWhatItShould(t *testing.T) {
	h := withHosts(&SiteHandler{ExportKey: NewExportKey(), Limits: config.DefaultLimits()})
	now := time.Now()
	good := h.SignExportToken("user-1", "site-1", now.Add(h.Limits.ExportLinkTTL))
	if u, s, err := h.CheckExportToken(good, now); err != nil || u != "user-1" || s != "site-1" {
		t.Fatalf("good token refused: %v %q %q", err, u, s)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(good)
	raw[len(raw)/2] ^= 1
	swapped := base64.RawURLEncoding.EncodeToString(raw)
	other := withHosts(&SiteHandler{ExportKey: NewExportKey(), Limits: config.DefaultLimits()})
	for name, tok := range map[string]string{
		"expired":          h.SignExportToken("user-1", "site-1", now.Add(-time.Second)),
		"expires too late": h.SignExportToken("user-1", "site-1", now.Add(h.Limits.ExportLinkTTL+time.Minute)),
		"other site":       swapped,
		"another key":      other.SignExportToken("user-1", "site-1", now.Add(time.Minute)),
	} {
		if _, _, err := h.CheckExportToken(tok, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
