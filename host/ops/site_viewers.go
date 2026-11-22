package ops

import (
	"context"
	"database/sql"
	"log"
	"net/http"

	"github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

// RemoveSiteViewer removes a named viewer from a site and, in the same call,
// that person's push subscriptions for it: every caller gets the cleanup. A
// push failure is logged, never returned, since the viewer is already
// removed; delivery-time membership checking (ProcessDelivery) remains the
// backstop. push may be nil (no notifications on this server).
func RemoveSiteViewer(ctx context.Context, database *sql.DB, push *notify.Engine, siteID, email string) (bool, error) {
	removed, err := accountsrepo.RemoveSiteViewer(ctx, database, siteID, email)
	if err != nil || !removed {
		return removed, err
	}
	if push != nil {
		if pushErr := push.DeleteViewerPush(ctx, siteID, email); pushErr != nil {
			log.Printf("remove_site_viewer: push cleanup for site %s, %s: %v", siteID, email, pushErr)
		}
	}
	return removed, nil
}

// SiteAccess is who can open a site, as the owner's access and viewer
// routes answer it: access "anyone" or "specific", the named viewers, and
// what the call just changed.
type SiteAccess struct {
	Site              string             `json:"site"`
	Access            string             `json:"access"`
	Viewers           []model.SiteViewer `json:"viewers"`
	ViewerLimit       int                `json:"viewer_limit"`
	PasscodeProtected bool               `json:"passcode_protected"`
	Added             []string           `json:"added,omitempty"`
	AlreadyListed     []string           `json:"already_listed,omitempty"`
	Removed           string             `json:"removed,omitempty"`
	Note              string             `json:"note,omitempty"`
}

// The two access levels a site can have.
const (
	accessAnyone   = "anyone"
	accessSpecific = "specific"
)

// OwnSiteAccess is the access of userID's site name, for GET
// /v1/sites/{sitename}/access. limit is the most viewers a site can name.
func OwnSiteAccess(ctx context.Context, svc *sites.Service, userID, name string, limit int) (SiteAccess, error) {
	site, err := svc.GetSiteByUser(ctx, userID, name)
	if err != nil {
		return SiteAccess{}, err
	}
	return siteAccessState(ctx, svc, site, limit, SiteAccess{})
}

// siteAccessState is site's access as it is now, with change's Added,
// AlreadyListed, Removed and Note carried over.
func siteAccessState(ctx context.Context, svc *sites.Service, site model.Site, limit int, change SiteAccess) (SiteAccess, error) {
	row, err := svc.GetSitePasscode(ctx, site.ID)
	if err != nil {
		return SiteAccess{}, err
	}
	viewers, err := svc.ListSiteViewers(ctx, site.ID)
	if err != nil {
		return SiteAccess{}, err
	}
	level := accessAnyone
	if row.NamedViewers {
		level = accessSpecific
	}
	change.Site, change.Access, change.Viewers, change.ViewerLimit, change.PasscodeProtected = site.Name, level, viewers, limit, row.Enc != nil
	return change, nil
}

// SetSiteAccess sets site's access to level ("anyone" or "specific") for
// the owner by. specific: only the owner and the named viewers can open the
// site; anyone: everyone with the address again, the list kept for next
// time. gate is the site's address facts. The caller holds the site's lock.
func SetSiteAccess(ctx context.Context, svc *sites.Service, gate access.GateSite, site model.Site, level, by string, limit int) (SiteAccess, error) {
	row, err := svc.GetSitePasscode(ctx, site.ID)
	if err != nil {
		return SiteAccess{}, err
	}
	if level == accessSpecific {
		if err := turnOnNamedViewers(ctx, svc, gate, site, row, by); err != nil {
			return SiteAccess{}, err
		}
		return siteAccessState(ctx, svc, site, limit, SiteAccess{Note: "Only you and the named viewers can open the site now; everyone else sees a sign-in page. Viewers sign in on the site with the email you named."})
	}
	if row.NamedViewers {
		if err := svc.SetSiteAccess(ctx, site, row, accessAnyone); err != nil {
			return SiteAccess{}, err
		}
		log.Printf("site_access access=anyone site_id=%s name=%s by=%s", site.ID, site.Name, by)
	}
	return siteAccessState(ctx, svc, site, limit, SiteAccess{Note: "Anyone with the address can open the site. The named viewers are kept, in case you turn it back on."})
}

// NameSiteViewers adds the emails (normalized, access.ViewerEmails) not yet
// named and turns named viewers on: the list first, then the gate, so a
// failure in between leaves the site as it was, with the names ready. The
// caller holds the site's lock.
func NameSiteViewers(ctx context.Context, database *sql.DB, svc *sites.Service, gate access.GateSite, site model.Site, emails []string, by string, limit int) (SiteAccess, error) {
	row, err := svc.GetSitePasscode(ctx, site.ID)
	if err != nil {
		return SiteAccess{}, err
	}
	if !row.NamedViewers {
		if err := access.NamedViewersRefusal(row, gate); err != nil {
			return SiteAccess{}, err
		}
	}
	fresh, already, err := access.New(database).NewViewers(ctx, site.ID, emails, limit)
	if err != nil {
		return SiteAccess{}, err
	}
	for _, e := range fresh {
		if _, err := svc.AddSiteViewer(ctx, site.ID, e); err != nil {
			return SiteAccess{}, err
		}
	}
	if err := turnOnNamedViewers(ctx, svc, gate, site, row, by); err != nil {
		return SiteAccess{}, err
	}
	log.Printf("site_viewers added=%d site_id=%s name=%s by=%s", len(fresh), site.ID, site.Name, by)
	note := "Only you and the named viewers can open the site. Tell them to open its address and sign in with the email you named (an emailed code, or Google for a Google address)."
	return siteAccessState(ctx, svc, site, limit, SiteAccess{Added: fresh, AlreadyListed: already, Note: note})
}

// UnnameSiteViewer takes email off site's list (RemoveSiteViewer, push
// devices included); their next request is refused. Access stays as it is.
// An email not on the list is a viewer_not_found refusal. The caller holds
// the site's lock.
func UnnameSiteViewer(ctx context.Context, database *sql.DB, push *notify.Engine, svc *sites.Service, site model.Site, email, by string, limit int) (SiteAccess, error) {
	removed, err := RemoveSiteViewer(ctx, database, push, site.ID, email)
	if err != nil {
		return SiteAccess{}, err
	}
	if !removed {
		return SiteAccess{}, &access.Refusal{Status: http.StatusNotFound, Code: "viewer_not_found", Message: email + " is not a named viewer of this site"}
	}
	log.Printf("site_viewers removed=1 site_id=%s name=%s by=%s", site.ID, site.Name, by)
	return siteAccessState(ctx, svc, site, limit, SiteAccess{Removed: email, Note: email + " can no longer open the site."})
}

// turnOnNamedViewers sets access to specific: the marker first, then the
// database, so a failure in between leaves the site closed, never open.
func turnOnNamedViewers(ctx context.Context, svc *sites.Service, gate access.GateSite, site model.Site, row model.SitePasscodeRow, by string) error {
	if row.NamedViewers {
		return nil
	}
	if err := access.NamedViewersRefusal(row, gate); err != nil {
		return err
	}
	if err := svc.SetSiteAccess(ctx, site, row, accessSpecific); err != nil {
		return err
	}
	log.Printf("site_access access=specific site_id=%s name=%s by=%s", site.ID, site.Name, by)
	return nil
}
