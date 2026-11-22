package servertest

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/domains"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

type NoticeMailer struct {
	mu   sync.Mutex
	Sent []string
}

func (m *NoticeMailer) SendSignInCode(string, string, string) error { return nil }
func (m *NoticeMailer) SendNotice(to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sent = append(m.Sent, to+"|"+subject+"|"+text)
	return nil
}

func Uniq(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (a *PrivateApp) DomainInfo(t *testing.T, siteID string) model.SiteDomainInfo {
	t.Helper()
	info, _, err := sitesrepo.GetSiteDomainInfo(context.Background(), a.Database, siteID)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func (a *PrivateApp) RedirectMarker(t *testing.T, p Person, site string) string {
	t.Helper()
	uid, _ := a.UserID(t, p)
	b, err := os.ReadFile(filepath.Join(a.Sites.Disk.SiteDir(uid, site), "domain-redirect"))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

// verify stands in for a passing check (DNS here, certificate live, HTTPS 2xx).
func (a *PrivateApp) Verify(t *testing.T, siteID, domain string) {
	t.Helper()
	info := a.DomainInfo(t, siteID)
	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(context.Background(), model.BoundDomain{
		SiteID: siteID, Domain: domain, Status: info.Status, Verified: info.VerifiedAt.Valid, PreviousDomain: info.PreviousDomain,
	}, "active", "", "live")
}
