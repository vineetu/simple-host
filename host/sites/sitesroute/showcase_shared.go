package sitesroute

import (
	"context"
	"io"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	xhtml "golang.org/x/net/html"
)

// MainSiteURL is the apex app address (https://<site domain>).
func (h *Handler) MainSiteURL() string {
	d := h.SiteDomain
	if d == "" {
		d = "simple-host.app"
	}
	return "https://" + d
}

// ContentBaseURL is the absolute base for user site and showcase links: always the content host,
// whichever origin served the page.
func (h *Handler) ContentBaseURL() string {
	host := h.ContentHost
	if host == "" {
		host = "sites." + h.SiteDomain
	}
	return "https://" + host
}

// PublicShowcaseData is what a person's public showcase shows: the bio and
// their public, open sites with title and description, pinned first.
func (h *Handler) PublicShowcaseData(ctx context.Context, user model.User) (web.ShowcaseData, error) {
	sites, err := h.SitesSvc().ListSitesByUser(ctx, user.ID)
	if err != nil {
		return web.ShowcaseData{}, err
	}
	bio, err := h.StoreAccounts().GetShowcaseBio(ctx, user.ID)
	if err != nil {
		return web.ShowcaseData{}, err
	}
	prefs, err := h.SitesSvc().ShowcasePreferences(ctx, user.ID)
	if err != nil {
		return web.ShowcaseData{}, err
	}
	handle := user.Handle.String
	data := web.ShowcaseData{Bio: bio, Handle: handle, SitesBaseURL: h.ContentBaseURL(), PublicShowcaseURL: h.PersonPageURL(handle), OwnerAppURL: h.MainSiteURL() + "/" + handle, MainURL: h.MainSiteURL(), Sites: []web.ShowcaseSite{}}
	for _, s := range sites {
		if s.Visibility != "public" || !s.OpenToAnyone() {
			continue
		}
		title, desc := h.showcaseMetadata(s)
		data.Sites = append(data.Sites, web.ShowcaseSite{Name: s.Name, URL: h.SiteURL(handle, s.Name), CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Visibility: "public", Title: title, Description: desc, Pinned: prefs[s.ID].Pinned, Order: prefs[s.ID].Order})
	}
	sort.SliceStable(data.Sites, func(i, j int) bool {
		if data.Sites[i].Pinned != data.Sites[j].Pinned {
			return data.Sites[i].Pinned
		}
		return data.Sites[i].Order < data.Sites[j].Order
	})
	return data, nil
}

// Metadata comes only from a visible site's deployed index, never a remote fetch.
func (h *Handler) showcaseMetadata(s model.Site) (title, description string) {
	f, _, err := h.Disk.OpenFile(context.Background(), sites.FileVersion{UserID: s.UserID, SiteName: s.Name}, "index.html")
	if err != nil {
		return "", ""
	}
	defer f.Close()
	z := xhtml.NewTokenizer(io.LimitReader(f, 256<<10))
	inTitle := false
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			return strings.TrimSpace(title), strings.TrimSpace(description)
		case xhtml.StartTagToken, xhtml.SelfClosingTagToken:
			t := z.Token()
			if t.Data == "title" {
				inTitle = true
			}
			if t.Data == "meta" {
				var name, content string
				for _, a := range t.Attr {
					if a.Key == "name" {
						name = strings.ToLower(a.Val)
					}
					if a.Key == "content" {
						content = a.Val
					}
				}
				if name == "description" && description == "" {
					description = content
				}
			}
		case xhtml.EndTagToken:
			if z.Token().Data == "title" {
				inTitle = false
			}
		case xhtml.TextToken:
			if inTitle {
				title += string(z.Text())
			}
		}
	}
}
