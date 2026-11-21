package siteshttp

import (
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/web"
)

// The Keys panel states KEY_IDLE_EXPIRY_DAYS, including when it is off.
func TestKeysPanelIdleCopyFollowsSetting(t *testing.T) {
	for v, want := range map[string]string{
		"90": "A key unused for 90 days stops working.",
		"0":  "Unused keys keep working until revoked.",
	} {
		l := withLimits(t, map[string]string{"KEY_IDLE_EXPIRY_DAYS": v})
		page, err := chromeTestHandlerFor(edition.Default(l)).Pipeline().BuildChromePage("showcase.html", web.ChromeData{})
		if err != nil {
			t.Fatal(err)
		}
		assertOwnerPanel(t, ownerPageElements(t, string(page)), "owner-keys", "owner-tab-settings")
		if !strings.Contains(string(page), want) || strings.Contains(string(page), "unused for 180 days") {
			t.Errorf("KEY_IDLE_EXPIRY_DAYS=%s: Keys panel does not say %q", v, want)
		}
	}
}
