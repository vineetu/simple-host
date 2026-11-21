package servertest

import (
	"testing"

	model "github.com/vsriram/simple-host/host/model"
)

func (a *PrivateApp) BoundDomain(t *testing.T, siteID string) model.BoundDomain {
	t.Helper()
	return model.BoundDomainOf(a.DomainInfo(t, siteID))
}
