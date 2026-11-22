package public

import (
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	core "github.com/vsriram/simple-host/products/hack/core"
)

// getDirectory serves the public directory groups (events.Directory), with
// the url/icon_url/color/apply_url fields from this instance's config.
func (h *handler) getDirectory(w http.ResponseWriter, r *http.Request) {
	dir, err := h.Events.Directory(r.Context(), time.Now)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	groups := map[string][]map[string]any{
		"now":      hackDirectoryRows(h, dir.Now),
		"upcoming": hackDirectoryRows(h, dir.Upcoming),
		"past":     hackDirectoryRows(h, dir.Past),
	}
	httpx.WriteJSON(w, 200, groups)
}

// hackDirectoryRows builds getDirectory's JSON rows for one group: the
// service's typed Row plus the url/icon_url/color/apply_url fields only the
// transport can compute.
func hackDirectoryRows(h *handler, rows []events.Row) []map[string]any {
	out := []map[string]any{}
	for _, ev := range rows {
		row := map[string]any{
			"slug": ev.Slug, "title": ev.Title, "tagline": ev.Tagline, "stage": ev.Stage,
			"time_zone": ev.TimeZone, "starts_at": core.RFC3339(ev.StartsAt), "ends_at": core.RFC3339(ev.EndsAt),
			"url": h.Links.EventURL(ev.Slug), "icon_url": h.Links.IconURLSlug(ev.Slug), "color": core.EventColor(ev.Slug, ev.AccentColor),
		}
		row["applications_open"] = ev.ApplicationsOpen
		if ev.ApplicationsOpen {
			row["apply_url"] = strings.TrimRight(h.Links.PublicBaseURL, "/") + "/e/" + ev.Slug + "/apply"
		}
		out = append(out, row)
	}
	return out
}
