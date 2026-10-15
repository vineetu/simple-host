package wpimport

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type menuItem struct {
	ID, Parent, Title, URL string
	Order                  int
	Children               []*menuItem
}

// buildMenu picks the site's default menu (same heuristic as before: a
// preferred name, else the largest group) and, as a side effect, works out
// which nav_menu group belongs to each language (the group whose post_type
// targets are mostly in that language), stashed in c.perLangMenu for pages
// that have their own language.
func (c *converter) buildMenu() []*menuItem {
	groups := map[string][]*menuItem{}
	groupLang := map[string]map[string]int{}
	for _, i := range c.data.Items {
		if i.Type != "nav_menu_item" || !i.public() {
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
		var target *item
		switch i.metadata("_menu_item_type") {
		case "post_type":
			if o := c.byID[i.metadata("_menu_item_object_id")]; o != nil {
				u = o.Path
				if o.Type == "attachment" {
					if c.attachment(o.ID) == nil {
						continue
					}
					u = o.Attachment
				}
				target = o
			}
		case "taxonomy":
			for _, t := range c.data.Terms {
				if t.ID == i.metadata("_menu_item_object_id") && c.archiveTerms[t.Taxonomy+":"+t.Slug] {
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
				if o.public() && o.Path != "" || c.attachment(o.ID) == o {
					title = o.Title
				}
			}
		}
		groups[group] = append(groups[group], &menuItem{ID: i.ID, Parent: i.metadata("_menu_item_menu_item_parent"), Title: title, URL: u, Order: i.Order})
		if target != nil {
			if l := target.language(); l != "" {
				if groupLang[group] == nil {
					groupLang[group] = map[string]int{}
				}
				groupLang[group][l]++
			}
		}
	}
	built := map[string][]*menuItem{}
	build := func(key string) []*menuItem {
		if r, ok := built[key]; ok {
			return r
		}
		items := groups[key]
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
		built[key] = roots
		return roots
	}
	scoreGroup := func(k string) int {
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

		return score
	}
	selected := ""
	best := -1
	for _, k := range sortedKeys(groups) {
		score := scoreGroup(k)
		if score > best {
			selected = k
			best = score
		}
	}
	var def []*menuItem
	if selected != "" {
		def = build(selected)
	} else {
		for _, i := range c.data.Items {
			if i.Type == "wp_navigation" {
				if m := c.navigationItems(parseBlocks(i.Content)); len(m) > 0 && i.public() {
					c.headerRef = i.ID
					def = m
					break
				}
			}
		}
		if def == nil {
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
			def = roots
		}
	}
	perLang := map[string][]*menuItem{}
	perLangScore := map[string]int{}
	for _, group := range sortedKeys(groupLang) {
		langs := groupLang[group]
		bestLang, bestCount := "", 0
		for _, l := range sortedKeys(langs) {
			if langs[l] > bestCount {
				bestLang, bestCount = l, langs[l]
			}
		}
		if bestLang == "" {
			continue
		}
		if score := scoreGroup(group); perLang[bestLang] == nil || score > perLangScore[bestLang] {
			perLangScore[bestLang] = score
			perLang[bestLang] = build(group)
		}
	}
	c.perLangMenu = perLang
	return def
}

// menuFor returns the menu built for lang, falling back to the site's
// default menu when that language has none of its own (or lang is "").
func (c *converter) menuFor(lang string) []*menuItem {
	if lang != "" {
		if m, ok := c.perLangMenu[lang]; ok {
			return m
		}
	}
	return c.headerMenu
}

// mapMenu rebuilds a menu tree, replacing or dropping items through f (a nil
// result drops the item; any other result, possibly unchanged, is kept with
// its children processed the same way).
func mapMenu(m []*menuItem, f func(*menuItem) *menuItem) []*menuItem {
	if len(m) == 0 {
		return m
	}
	out := make([]*menuItem, 0, len(m))
	for _, it := range m {
		r := f(it)
		if r == nil {
			continue
		}
		cp := *r
		cp.Children = mapMenu(r.Children, f)
		out = append(out, &cp)
	}
	return out
}

// resolveMenuForPage replaces Polylang's dead "#pll_switcher" custom link
// (one is left in the menu per language) with real links to the current
// item's translations, or drops it when there is only one language.
func (c *converter) resolveMenuForPage(m []*menuItem, i *item) []*menuItem {
	return mapMenu(m, func(it *menuItem) *menuItem {
		if it.URL != "#pll_switcher" {
			return it
		}
		if !c.multiLang {
			return nil
		}
		links := c.switcherLinks(i)
		if len(links) == 0 {
			return nil
		}
		return &menuItem{Title: it.Title, Children: links}
	})
}

var languageDisplayNames = map[string]string{
	"en": "English", "es": "Español", "fr": "Français", "de": "Deutsch",
	"it": "Italiano", "pt": "Português", "nl": "Nederlands", "ja": "日本語", "zh": "中文",
}

func languageName(code string) string {
	if n, ok := languageDisplayNames[code]; ok {
		return n
	}
	return strings.ToUpper(code)
}

// switcherLinks lists, for every language the site has, a link to i's
// translation in that language (or, when i has none, that language's front
// page), skipping any language with no public target. i may be nil (a page
// not tied to a single translatable item), in which case every language
// falls straight back to its front page.
func (c *converter) switcherLinks(i *item) []*menuItem {
	if !c.multiLang {
		return nil
	}
	group := c.translationGroup(i)
	var out []*menuItem
	for _, l := range sortedKeys(c.languages) {
		target := group[l]
		if target == nil {
			target = c.frontTranslations[l]
		}
		if target == nil || !target.public() {
			continue
		}
		out = append(out, &menuItem{Title: languageName(l), URL: target.Path})
	}
	return out
}

// translationGroup finds i's Polylang translation set (post_translations
// category, the matching term's PHP-serialized description) and returns the
// language-to-item map, or nil when i has none.
func (c *converter) translationGroup(i *item) map[string]*item {
	if i == nil {
		return nil
	}
	for _, cat := range i.Categories {
		if cat.Domain != "post_translations" {
			continue
		}
		t, ok := c.terms["post_translations:"+cat.Slug]
		if !ok {
			continue
		}
		ids := parsePolylangMap(t.Description)
		if len(ids) == 0 {
			continue
		}
		out := map[string]*item{}
		for lang, id := range ids {
			if target := c.byID[id]; target != nil {
				out[lang] = target
			}
		}
		return out
	}
	return nil
}

var phpSerializedPair = regexp.MustCompile(`s:(\d+):"([^"]*)";i:(-?\d+);`)

// parsePolylangMap reads a PHP-serialized string-to-int map, such as
// Polylang's post_translations term description
// (a:2:{s:2:"en";i:12;s:2:"es";i:34;}), into language code to item id. Each
// pair's declared byte length must match the string found, a cheap bounds
// check against truncated or malformed input.
func parsePolylangMap(s string) map[string]string {
	out := map[string]string{}
	for _, m := range phpSerializedPair.FindAllStringSubmatch(s, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil || n != len(m[2]) {
			continue
		}
		out[m[2]] = m[3]
	}
	return out
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

// menuHasPath reports whether some entry of m (at any depth) links to p.
func menuHasPath(m []*menuItem, p string) bool {
	for _, i := range m {
		if i.URL == p || menuHasPath(i.Children, p) {
			return true
		}
	}
	return false
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
		if i.URL == "" {
			link = esc(i.Title)
		}
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
