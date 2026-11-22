package sites

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/vsriram/simple-host/host/sites/repo"

	"github.com/vsriram/simple-host/host/model"
)

// LockAccountSites takes the lock of every site of the account and of the
// teams it belongs to (accounts.AccountSiteKeys) before the account is erased.
func (s *Service) LockAccountSites(ctx context.Context, userID string) (func(), error) {
	var unlocks []func()
	release := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
		unlocks = nil
	}
	byKey := func(a, b [2]string) int {
		if c := strings.Compare(a[0], b[0]); c != 0 {
			return c
		}
		return strings.Compare(a[1], b[1])
	}
	names, err := repo.AccountSiteKeys(ctx, s.DB, userID)
	for try := 0; err == nil; try++ {
		slices.SortFunc(names, byKey)
		for _, n := range names {
			unlocks = append(unlocks, s.LockSite(n[0], n[1]))
		}
		var again [][2]string
		again, err = repo.AccountSiteKeys(ctx, s.DB, userID)
		if err != nil {
			break
		}
		slices.SortFunc(again, byKey)
		if slices.Equal(names, again) || try == 4 {
			return release, nil
		}
		release()
		names = again
	}
	release()
	return func() {}, err
}

func (svc *Service) EraseAccountFiles(ctx context.Context, e model.ErasedAccount, hosts *Hosts) error {
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, s := range e.Sites {
		keep(svc.Disk.PurgeTrashedSiteLinks(e.UserID, s.Name, ""))
		for _, d := range s.Domains {
			mine, err := svc.Disk.UnbindDomainOf(d, e.UserID, s.Name)
			keep(err)
			if err != nil || !mine {
				continue
			}
			if _, err := svc.GetSiteByCustomDomain(ctx, d); errors.Is(err, sql.ErrNoRows) {
				hosts.CancelDomainCert(d)
			}
		}
	}
	for _, a := range e.Aliases {
		keep(svc.Disk.RemoveHandleLinkOf(a, e.UserID))
	}
	// Address families: the link (only while it is still this account's)
	// and the issuer's request, so nothing is served there any more.
	for _, f := range e.Families {
		if svc.Disk.FamilyLinkTarget(f) == filepath.Join("..", "by-id", e.UserID) {
			keep(svc.Disk.UnbindFamily(f))
		}
		hosts.RemoveFamilyRequest(context.Background(), f)
	}
	if len(e.Families) > 0 {
		hosts.RefreshFamilies(ctx)
	}
	keep(svc.Disk.DeleteUser(e.UserID, e.Handle))
	return first
}
