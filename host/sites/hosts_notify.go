package sites

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

// EventScoped: notifications are scoped to events, not sites.
func (h *Hosts) EventScoped() bool { return h.eventScoped }

// SiteEvent is one site event to queue: Data["visitor"], when set, is who it
// is about; Dedupe > 0 drops a repeat for them within that window.
type SiteEvent struct {
	SiteID, Name string
	Data         map[string]any
	Dedupe       time.Duration
}

// NotificationSite is the site's name and address as notification payloads
// show them. Hack's organiser website uses an internal UUID for storage;
// payloads use the event slug and public address, as the website tools do.
func (h *Hosts) NotificationSite(ctx context.Context, site model.Site) (string, string, error) {
	if !h.eventScoped {
		url := h.addr.SiteURL(site.OwnerHandle, site.Name)
		if url == "" {
			url = site.SiteURL
		}
		return site.Name, url, nil
	}
	if h.groups == nil {
		return "", "", sql.ErrNoRows
	}
	ev, err := h.groups.ByAccount(ctx, site.UserID)
	if err != nil {
		return "", "", err
	}
	if site.Name == ev.ID {
		return ev.Slug, "https://" + ev.Slug + "." + h.addr.SiteDomain + "/", nil
	}
	return site.Name, h.addr.TeamSiteURL(ev.Slug, site.Name), nil
}
