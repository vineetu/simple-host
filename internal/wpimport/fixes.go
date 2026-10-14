package wpimport

import (
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

const footerPage = "every page (site footer)"

func dateHTML(raw string) string {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339, time.RFC1123Z, "2006-01-02"} {
		if d, err := time.Parse(layout, raw); err == nil {
			return `<time datetime="` + d.Format(time.RFC3339) + `">` + d.Format("2 January 2006") + `</time>`
		}
	}
	return `<time datetime="` + esc(raw) + `">` + esc(raw) + `</time>`
}

func inWidget(n *xhtml.Node) bool {
	for ; n != nil; n = n.Parent {
		if getAttr(n, "data-sh-feature") != "" {
			return true
		}
	}
	return false
}
func nodeHTML(n *xhtml.Node) string {
	var s strings.Builder
	_ = xhtml.Render(&s, n)
	return s.String()
}
func (c *converter) counterWords(page, words string) {
	if strings.TrimSpace(words) == "" {
		return
	}
	f := c.features[FeatureHitCounter]
	if f != nil && !strings.Contains(f.Note, page) {
		if f.Note == "" {
			f.Note = "Words around a removed counter remain on: "
		}
		f.Note += page + "; "
	}
}
func (c *converter) contentHasImage(body, raw string) bool {
	wanted := c.media[raw]
	if wanted == "" {
		wanted = c.mediaPath(raw)
	}
	found := false
	walk(fragment(body), func(n *xhtml.Node) {
		if n.Data == "img" {
			src := getAttr(n, "src")
			p := c.media[src]
			if p == "" {
				p = c.mediaPath(src)
			}
			if src == raw || wanted != "" && p == wanted {
				found = true
			}
		}
	})
	return found
}
func stripTemplateBlocks(bs []*block) []*block {
	var out []*block
	for _, b := range bs {
		switch b.Name {
		case "core/navigation", "core/navigation-link", "core/navigation-submenu", "core/site-title", "core/site-logo":
			continue
		}
		b.Children = stripTemplateBlocks(b.Children)
		out = append(out, b)
	}
	return out
}
func (c *converter) templateParts() {
	for _, i := range c.data.Items {
		if i.Type != "wp_template_part" {
			continue
		}
		page := "every page (site header)"
		footer := strings.Contains(strings.ToLower(i.Slug+" "+i.Title), "footer")
		if footer {
			page = footerPage
		}
		body := c.renderBlocks(stripTemplateBlocks(parseBlocks(i.Content)), page, 0)
		body = c.cleanContent(body, page)
		if footer {
			root := fragment(body)
			rootURL := func(raw string) string {
				if raw != "" && !strings.Contains(raw, ":") && !strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "#") {
					return "/" + raw
				}
				return raw
			}
			rootCSS := func(css string) string {
				return cssURL.ReplaceAllStringFunc(css, func(raw string) string {
					m := cssURL.FindStringSubmatch(raw)
					return `url("` + rootURL(strings.TrimSpace(m[1])) + `")`
				})
			}
			walk(root, func(n *xhtml.Node) {
				for _, k := range []string{"href", "src", "poster", "action"} {
					raw := getAttr(n, k)
					if raw != "" {
						setAttr(n, k, rootURL(raw))
					}
				}
				if style := getAttr(n, "style"); style != "" {
					setAttr(n, "style", rootCSS(style))
				}
				if n.Data == "style" {
					for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
						ch.Data = rootCSS(ch.Data)
					}
				}
			})
			c.footer += serialize(root)
		}
	}
}
