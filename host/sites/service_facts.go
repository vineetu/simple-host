package sites

import (
	"context"
	"fmt"
	"log"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
)

// lockSite takes the per-site upload lock (Deps.Locks).
func (s *Service) LockSite(userID, name string) func() {
	return LockSiteName(s.Deps.Locks, userID, name)
}

// lockAccount takes the account's file-caps lock, the site lock under the
// empty name; without file caps there is nothing to serialize.
func (s *Service) LockAccount(userID string) func() {
	if !s.Deps.FileCaps {
		return func() {}
	}
	return s.LockSite(userID, "")
}

// requestCert asks for the handle's site-host certificate.
func (s *Service) RequestCert(handle string) {
	if s.Deps.Hosts != nil {
		s.Deps.Hosts.RequestSiteCert(context.Background(), handle)
	}
}

// syncFamilyRedirect brings the site's address-family redirect up to date
// when any family is live.
func (s *Service) SyncFamilyRedirect(ctx context.Context, siteID string) {
	if s.Deps.Hosts != nil && s.Deps.Hosts.HasLiveFamilies() {
		s.Deps.Hosts.SyncDomainRedirect(ctx, siteID)
	}
}

// syncDomainRedirect rewrites the site's domain redirect marker.
func (s *Service) SyncDomainRedirect(ctx context.Context, siteID string) {
	if s.Deps.Hosts != nil {
		s.Deps.Hosts.SyncDomainRedirect(ctx, siteID)
	}
}

// renameURL is the address a renamed site is handed out at.
func (s *Service) RenameURL(handle, name string) string {
	url := s.Deps.Hosts.Addressing().SiteURL(handle, name)
	if url == "" {
		url = fmt.Sprintf("https://%s/%s/%s/", s.Deps.ContentHost, handle, name)
	}
	return url
}

// namedViewersRefusal is Access's refusal of named viewers for a new site.
func (s *Service) NamedViewersRefusal(handle, name string) error {
	site := model.Site{OwnerHandle: handle, Name: name}
	return accountapp.NamedViewersRefusal(model.SitePasscodeRow{}, s.Deps.Hosts.GateSite(site, s.Deps.NoVisitorSignIn))
}

// DeletedNameMessage tells the owner a name is held by their site in
// Recently deleted until it is purged.
func DeletedNameMessage(d model.DeletedSite, retention time.Duration) string {
	return "you deleted a site named " + d.Name + " recently; it is in Recently deleted until " +
		d.PurgeAt(retention).UTC().Format("2 Jan 2006") + ". Restore it, or pick another name"
}

// deletedNameConflict reports whether name is held by the account's site in
// Recently deleted, and the message saying so.
func (s *Service) DeletedNameConflict(ctx context.Context, userID, name string) (string, bool) {
	d, err := s.GetDeletedSiteByUser(ctx, userID, name)
	if err != nil {
		return "", false
	}
	return DeletedNameMessage(d, s.Deps.DeletedRetention), true
}

// retainedFloor is the lowest version retention keeps; an instance with
// event version retention keeps the latest versions below the active one.
func (s *Service) RetainedFloor(versions []model.Version, keep, active, siteKeep int) int {
	if s.Deps.EventVersionRetention {
		floor, _ := pruneThreshold(active, EffectiveKeepVersions(siteKeep, s.Deps.DefaultRetention))
		return floor
	}
	return RetainedFloor(versions, keep)
}

// pinnedFloor lowers a prune threshold so a team's deadline version is
// kept whatever the retention.
func (s *Service) PinnedFloor(ctx context.Context, userID, siteName string, keepFrom int) int {
	if s.Deps.Hosts == nil || s.Deps.Hosts.Groups() == nil {
		return keepFrom
	}
	v, ok, err := s.Deps.Hosts.Groups().PinnedVersion(ctx, userID, siteName)
	if err != nil {
		log.Printf("prune versions for %s: pinned version: %v", siteName, err)
		return 0 // keep everything rather than guess
	}
	if ok && v < keepFrom {
		return v
	}
	return keepFrom
}

// The active version is never a candidate, which matters after a rollback: an
// instance keeping one version whose active is v3 out of v10 must not delete
// v4 through v10, because active is below them. It keeps more than asked for
// until the next deploy, which then makes active the newest again and clears
// the backlog in one pass. Keeping too much is recoverable; deleting the tree
// someone is looking at is not.
func pruneThreshold(activeVersion, keep int) (keepFrom int, ok bool) {
	if keep <= 0 {
		return 0, false
	}
	keepFrom = activeVersion - keep + 1
	if keepFrom < 2 {
		return 0, false // nothing below v1 exists to remove
	}
	return keepFrom, true
}
