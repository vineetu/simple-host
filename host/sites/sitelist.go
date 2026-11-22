package sites

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/analytics"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// SiteListInput is a GET /v1/sites: who asks, whether an operator widened it
// to every account's sites (the route refuses that to anyone else), the
// filter and paging parameters (FilterSiteList), and whether the analytics
// log is read on this server (Views).
type SiteListInput struct {
	User   *model.User
	All    bool
	Params url.Values
	Views  bool
}

// SiteListRow is one site in the list with what the list shows beside it.
type SiteListRow struct {
	Site model.Site
	// Views are people's views over 7 and 30 days, the owner's own visits
	// excluded; nil where the analytics log is not read (nobody measured).
	Views         *analytics.SiteViews
	FileBytes     int64
	Pinned        bool
	Order         int
	Keep          bool
	Owner, Access string // a team member's list: the team's handle and "member" or "editor"
}

// ListSites is the caller's sites (or, with All, every account's), narrowed
// to the sites a site editor may edit, filtered and paged, with their views,
// footprint on disk, showcase pin and order and idle keep mark. Refusals:
// *Error with the filter's codes (bad_order, bad_date, bad_pagination), and
// measure_failed when a site's files cannot be measured.
func (s *Service) ListSites(ctx context.Context, in SiteListInput) ([]SiteListRow, error) {
	list, err := s.listedSites(ctx, in)
	if err != nil {
		return nil, err
	}
	if list, err = FilterSiteList(list, in.Params); err != nil {
		return nil, err
	}
	flagsFor := in.User.ID
	if in.All {
		flagsFor = ""
	}
	kept, err := repo.ListSiteKeep(ctx, s.database, flagsFor)
	if err != nil {
		return nil, err
	}
	prefs, err := repo.ShowcasePreferences(ctx, s.database, flagsFor)
	if err != nil {
		return nil, err
	}
	var views map[string]analytics.SiteViews
	if in.Views {
		if views, err = analytics.PersonViewsBySite(ctx, s.database, flagsFor, in.All); err != nil {
			return nil, err
		}
	}
	rows := make([]SiteListRow, 0, len(list))
	for _, site := range list {
		footprint, err := s.disk.SiteFootprint(site.UserID, site.Name)
		if err != nil {
			return nil, &Error{Code: "measure_failed", Message: "could not measure website files"}
		}
		row := SiteListRow{Site: site, FileBytes: footprint.Total(), Pinned: prefs[site.ID].Pinned, Order: prefs[site.ID].Order, Keep: kept[site.ID]}
		if views != nil {
			v := views[site.ID] // zero when the site had no people views
			row.Views = &v
		}
		if m := in.User.Member; m != nil {
			row.Owner, row.Access = in.User.Handle.String, "member"
			if m.Edits != nil {
				row.Access = "editor"
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// listedSites is every site the list may show: the caller's own, or every
// account's, and for a site editor only the sites they edit.
func (s *Service) listedSites(ctx context.Context, in SiteListInput) ([]model.Site, error) {
	var list []model.Site
	var err error
	if in.All {
		list, err = repo.ListAllSites(ctx, s.database)
	} else {
		list, err = repo.ListSitesByUser(ctx, s.database, in.User.ID)
	}
	if err != nil {
		return nil, err
	}
	if m := in.User.Member; m != nil && m.Edits != nil {
		kept := list[:0]
		for _, site := range list {
			if slices.Contains(m.Edits, strings.ToLower(site.Name)) {
				kept = append(kept, site)
			}
		}
		list = kept
	}
	return list, nil
}
