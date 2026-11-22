package export

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

func TestExportTokenRefusesWhatItShould(t *testing.T) {
	h := &sitesroute.Handler{ExportKey: sitesroute.NewExportKey(), Limits: config.DefaultLimits()}
	now := time.Now()
	good := SignExportToken(h, "user-1", "site-1", now.Add(h.Limits.ExportLinkTTL))
	if u, s, err := CheckExportToken(h, good, now); err != nil || u != "user-1" || s != "site-1" {
		t.Fatalf("good token refused: %v %q %q", err, u, s)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(good)
	raw[len(raw)/2] ^= 1
	swapped := base64.RawURLEncoding.EncodeToString(raw)
	other := &sitesroute.Handler{ExportKey: sitesroute.NewExportKey(), Limits: config.DefaultLimits()}
	for name, tok := range map[string]string{
		"expired":          SignExportToken(h, "user-1", "site-1", now.Add(-time.Second)),
		"expires too late": SignExportToken(h, "user-1", "site-1", now.Add(h.Limits.ExportLinkTTL+time.Minute)),
		"other site":       swapped,
		"another key":      SignExportToken(other, "user-1", "site-1", now.Add(time.Minute)),
	} {
		if _, _, err := CheckExportToken(h, tok, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
