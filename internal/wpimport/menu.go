package wpimport

import (
	"net/url"
	"sort"
	"strings"
)

type menuItem struct {
	ID, Parent, Title, URL string
	Order                  int
	Children               []*menuItem
}

func (c *converter) buildMenu() []*menuItem {
	groups := map[string][]*menuItem{}
	for _, i := range c.data.Items {
		if i.Type != "nav_menu_item" {
			continue
		}
		group := "menu"
		for _, cat := range i.Categories {
			if cat.Domain == "nav_menu" {
				group = cat.Slug
				break
			}
		}
		u := i.metadata("_menu_item_url")
		switch i.metadata("_menu_item_type") {
		case "post_type":
			if o := c.byID[i.metadata("_menu_item_object_id")]; o != nil {
				u = o.Path
				if o.Type == "attachment" {
					u = o.Attachment
				}
			}
		case "taxonomy":
			for _, t := range c.data.Terms {
				if t.ID == i.metadata("_menu_item_object_id") {
					prefix := "category"
					if t.Taxonomy == "product_cat" {
						prefix = "product-category"
					}
					if t.Taxonomy == "post_tag" {
						prefix = "tag"
					}
					u = "/" + prefix + "/" + c.termPath(t, map[string]bool{}) + "/"
					break
				}
			}
		}
		if u == "" {
			continue
		}
		title := i.Title
		if title == "" {
			if o := c.byID[i.metadata("_menu_item_object_id")]; o != nil {
				title = o.Title
			}
		}
		groups[group] = append(groups[group], &menuItem{ID: i.ID, Parent: i.metadata("_menu_item_menu_item_parent"), Title: title, URL: u, Order: i.Order})
	}
	selected := ""
	best := -1
	for _, k := range sortedKeys(groups) {
		score := len(groups[k])
		name := k
		for _, t := range c.data.Terms {
			if t.Taxonomy == "nav_menu" && t.Slug == k {
				name += " " + t.Name
			}
		}
		preferred := strings.EqualFold(k, "menu")
		normalized := strings.NewReplacer("-", " ", "_", " ").Replace(strings.ToLower(name))
		for _, word := range strings.Fields(normalized) {
			if word == "primary" || word == "main" || word == "header" || word == "top" {
				preferred = true
			}
		}
		if preferred {
			score += 100000
		}

		if score > best {
			selected = k
			best = score
		}
	}
	if selected != "" {
		items := groups[selected]
		sort.SliceStable(items, func(a, b int) bool {
			if items[a].Order == items[b].Order {
				return items[a].ID < items[b].ID
			}
			return items[a].Order < items[b].Order
		})
		byID := map[string]*menuItem{}
		for _, i := range items {
			byID[i.ID] = i
		}
		var roots []*menuItem
		for _, i := range items {
			parent := byID[i.Parent]
			seen := map[string]bool{i.ID: true}
			cycle := false
			for p := parent; p != nil; p = byID[p.Parent] {
				if seen[p.ID] {
					cycle = true
					break
				}
				seen[p.ID] = true
			}
			if parent != nil && !cycle {
				parent.Children = append(parent.Children, i)
			} else {
				roots = append(roots, i)
			}
		}
		return roots
	}
	for _, i := range c.data.Items {
		if i.Type == "wp_navigation" {
			if m := c.navigationItems(parseBlocks(i.Content)); len(m) > 0 && i.public() {
				c.headerRef = i.ID
				return m
			}
		}
	}
	var roots []*menuItem
	for _, i := range c.published {
		if i.Type == "page" && (i.Parent == "" || i.Parent == "0") {
			roots = append(roots, &menuItem{Title: i.Title, URL: i.Path, Order: i.Order})
		}
	}
	sort.SliceStable(roots, func(a, b int) bool {
		if roots[a].Order == roots[b].Order {
			return roots[a].Title < roots[b].Title
		}
		return roots[a].Order < roots[b].Order
	})
	return roots
}

// navigationItems reads block-theme menu links. Like WordPress, a link to a
// page or post that is not public is left out (its label is often the
// draft's title); custom links are kept as written.
func (c *converter) navigationItems(bs []*block) []*menuItem {
	var out []*menuItem
	for _, b := range bs {
		if b.Name == "core/navigation-link" || b.Name == "core/navigation-submenu" {
			children := c.navigationItems(b.Children)
			u := attrString(b.Attrs, "url")
			if id := attrString(b.Attrs, "id"); id != "" && attrString(b.Attrs, "kind") != "taxonomy" && attrString(b.Attrs, "kind") != "custom" {
				if target := c.byID[id]; target != nil && target.Type != "attachment" {
					if !target.public() || target.Path == "" {
						if b.Name == "core/navigation-submenu" {
							out = append(out, children...)
						}
						continue
					}
					if target.Path != "" {
						u = target.Path
					}
				}
			}
			out = append(out, &menuItem{Title: attrString(b.Attrs, "label"), URL: u, Children: children})
		} else {
			out = append(out, c.navigationItems(b.Children)...)
		}
	}
	return out
}
func menuCount(m []*menuItem) int {
	n := len(m)
	for _, i := range m {
		n += menuCount(i.Children)
	}
	return n
}
func (c *converter) menuHTML(m []*menuItem, p string) string {
	if len(m) == 0 {
		return ""
	}
	s := "<ul>"
	for _, i := range m {
		u := i.URL
		if p != "" {
			u = c.rewriteURL(i.URL, p)
		}
		link := `<a href="` + esc(u) + `">` + esc(i.Title) + `</a>`
		if len(i.Children) > 0 {
			s += `<li><details><summary>` + link + `</summary>` + c.menuHTML(i.Children, p) + `</details></li>`
		} else {
			s += "<li>" + link + "</li>"
		}
	}
	return s + "</ul>"
}

func (c *converter) sameMenu(a, b []*menuItem) bool {
	urls := func(m []*menuItem) map[string]bool {
		out := map[string]bool{}
		var visit func([]*menuItem)
		visit = func(ms []*menuItem) {
			for _, i := range ms {
				key := i.URL
				if u, err := url.Parse(key); err == nil && (u.Host == "" || c.hosts[strings.ToLower(u.Hostname())]) {
					key = cleanPath(u.Path) + queryFragment(u)
				}
				out[key] = true
				visit(i.Children)
			}
		}
		visit(m)
		return out
	}
	x, y := urls(a), urls(b)
	if len(x) == 0 || len(x) != len(y) {
		return false
	}
	for u := range x {
		if !y[u] {
			return false
		}
	}
	return true
}
func (c *converter) responsiveNav(m []*menuItem, page, class string, search bool) string {
	wide := c.menuHTML(m, page)
	mobile := wide
	searchLink := ""
	if search {
		mobile = c.menuHTML(append(append([]*menuItem(nil), m...), &menuItem{Title: "Search", URL: "/search/"}), page)
		searchLink = `<a class="header-search" href="` + esc(relative(page, "/search/")) + `">Search</a>`
	}
	label := "Page menu"
	if search {
		label = "Main menu"
	}
	return `<nav class="` + class + `" aria-label="` + label + `"><div class="nav-wide">` + wide + `</div><details class="nav-mobile"><summary>Menu</summary>` + mobile + `</details></nav>` + searchLink
}
