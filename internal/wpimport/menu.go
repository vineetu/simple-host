package wpimport

import (
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
			if m := navigationItems(parseBlocks(i.Content)); len(m) > 0 {
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
func navigationItems(bs []*block) []*menuItem {
	var out []*menuItem
	for _, b := range bs {
		if b.Name == "core/navigation-link" || b.Name == "core/navigation-submenu" {
			out = append(out, &menuItem{Title: attrString(b.Attrs, "label"), URL: attrString(b.Attrs, "url"), Children: navigationItems(b.Children)})
		} else {
			out = append(out, navigationItems(b.Children)...)
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
