package sites

import (
	"strings"
	"testing"
	"time"
)

func TestPrepareImportLinkValidationOrder(t *testing.T) {
	// A malformed destination wins over a malformed theme, as the connector
	// refused it before decoding theme_url before this move.
	if _, _, err := PrepareImportLink("bad/name", "owner", 42, time.Minute, true); err == nil || !strings.Contains(err.Error(), "invalid site name") {
		t.Fatalf("destination error precedence: %v", err)
	}
	if _, _, err := PrepareImportLink("blog", "owner", 42, time.Minute, true); err == nil || err.Error() != "theme_url must be a string, got int" {
		t.Fatalf("theme error: %v", err)
	}
	// Link preparation is not a new-site claim: existing reserved names must
	// remain import destinations, and a theme is still trimmed.
	token, link, err := PrepareImportLink("api", "owner", " https://theme.example/ ", time.Minute, true)
	if err != nil || token == "" || link.ID == "" || link.Site != "api" || link.Owner != "owner" || link.ThemeURL != "https://theme.example/" {
		t.Fatalf("existing destination: %+v, %v", link, err)
	}
}
