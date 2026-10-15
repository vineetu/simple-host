package wpimport

import (
	"net/url"
	"sort"
	"strings"
	"time"
)

func internalType(s string) bool {
	if strings.HasPrefix(s, "wp_") || strings.HasPrefix(s, "acf-") || strings.HasPrefix(s, "_") {
		return true
	}
	switch s {
	case "", "attachment", "nav_menu_item", "revision", "custom_css", "customize_changeset", "oembed_cache", "user_request", "product_variation", "shop_order", "shop_order_refund", "shop_coupon", "shop_webhook", "wpcf7_contact_form", "wpforms", "wpforms-log", "give_forms", "give_payment", "elementor_library", "elementor_snippet", "et_pb_layout", "fl-builder-template", "mailpoet_page", "jp_pay_order", "jp_pay_product", "feedback", "scheduled-action", "tribe-ea-record", "deleted_event":
		return true
	}
	return false
}
func (c *converter) carries(i *item) bool {
	if isPageType(i.Type) {
		return true
	}
	if internalType(i.Type) {
		return false
	}
	u, err := url.Parse(i.Link)
	return err == nil && (u.Host == "" && u.Scheme == "" || c.hosts[strings.ToLower(u.Hostname())]) && u.Path != "" && u.Path != "/" && u.RawQuery == "" && u.Fragment == ""
}
func (c *converter) events() []*item {
	var out []*item
	for _, i := range c.published {
		if i.Type == "tribe_events" && i.public() {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		return out[a].metadata("_EventStartDate") < out[b].metadata("_EventStartDate")
	})
	return out
}
func eventWhen(i *item) string {
	start, err := time.Parse("2006-01-02 15:04:05", i.metadata("_EventStartDate"))
	if err != nil {
		return i.metadata("_EventStartDate")
	}
	end, endErr := time.Parse("2006-01-02 15:04:05", i.metadata("_EventEndDate"))
	date := start.Format("Monday 2 January 2006")
	allDay := strings.EqualFold(i.metadata("_EventAllDay"), "yes")
	if !allDay {
		date += ", " + start.Format("15:04")
	}
	if endErr == nil && end.After(start) {
		if start.Format("2006-01-02") != end.Format("2006-01-02") {
			date += " to " + end.Format("Monday 2 January 2006")
			if !allDay {
				date += ", " + end.Format("15:04")
			}
		} else if !allDay {
			date += " to " + end.Format("15:04")
		}
	}
	if allDay {
		date += ", all day"
	}
	return date
}
func (c *converter) eventDetails(i *item) string {
	s := ""
	if when := eventWhen(i); when != "" {
		s += "<p><strong>When:</strong> " + esc(when) + "</p>"
	}
	if venue := c.reusable(i.metadata("_EventVenueID"), "tribe_venue"); venue != nil {
		var parts []string
		for _, part := range []string{venue.Title, venue.metadata("_VenueAddress"), venue.metadata("_VenueCity")} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			s += "<p><strong>Where:</strong> " + esc(strings.Join(parts, ", ")) + "</p>"
		}
	}
	if organizer := c.reusable(i.metadata("_EventOrganizerID"), "tribe_organizer"); organizer != nil {
		s += "<p><strong>Organizer:</strong> " + esc(organizer.Title) + "</p>"
	}
	return s
}
func (c *converter) eventList() string {
	var s strings.Builder
	for _, i := range c.events() {
		s.WriteString(`<article><h2><a href="` + esc(i.Path) + `">` + esc(i.Title) + `</a></h2><p>` + esc(eventWhen(i)) + `</p></article>`)
	}
	return s.String()
}
