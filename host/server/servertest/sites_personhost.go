package servertest

import (
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
)

func NewPersonApp(t *testing.T, mode string, eds ...*edition.Edition) *PrivateApp {
	t.Helper()
	a := NewPrivateApp(t, eds...)
	a.Sites.Hosts.SetPersonHosts(mode)
	accountsrepo.SetPlatformDomains(PcSiteDomain)
	t.Cleanup(func() { accountsrepo.SetPlatformDomains("") })
	return a
}
