package wpimport

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

func esc(s string) string { return html.EscapeString(s) }
func fragment(s string) *xhtml.Node {
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	ns, err := xhtml.ParseFragment(strings.NewReader(s), root)
	if err == nil {
		for _, n := range ns {
			root.AppendChild(n)
		}
	}
	return root
}
func serialize(root *xhtml.Node) string {
	var b bytes.Buffer
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		liftVoidChildren(n)
		_ = xhtml.Render(&b, n)
	}
	return b.String()
}

// htmlVoid names are rendered as empty elements even inside SVG. A void
// element that still has children (a <br> that wpautop, or the source,
// left inside an <svg>, which fragment parsing does not close) makes Render
// write the start tag and stop before ">", so the following markup is glued
// on. Move those children to just after the element first.
var htmlVoid = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "keygen": true, "link": true,
	"meta": true, "param": true, "source": true, "track": true, "wbr": true,
}

func liftVoidChildren(n *xhtml.Node) {
	next := n.NextSibling
	for c := n.FirstChild; c != nil; {
		nxt := c.NextSibling
		liftVoidChildren(c)
		c = nxt
	}
	if n.Type != xhtml.ElementNode || !htmlVoid[n.Data] || n.FirstChild == nil || n.Parent == nil {
		return
	}
	parent := n.Parent
	for c := n.FirstChild; c != nil; {
		nxt := c.NextSibling
		n.RemoveChild(c)
		parent.InsertBefore(c, next)
		c = nxt
	}
}
func getAttr(n *xhtml.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}
func setAttr(n *xhtml.Node, k, v string) {
	for i := range n.Attr {
		if n.Attr[i].Key == k {
			n.Attr[i].Val = v
			return
		}
	}
	n.Attr = append(n.Attr, xhtml.Attribute{Key: k, Val: v})
}
func delAttr(n *xhtml.Node, k string) {
	a := n.Attr[:0]
	for _, v := range n.Attr {
		if v.Key != k {
			a = append(a, v)
		}
	}
	n.Attr = a
}
func walk(n *xhtml.Node, f func(*xhtml.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		walk(c, f)
		c = next
	}
}
func plain(s string) string {
	var b strings.Builder
	walk(fragment(s), func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
	})
	return strings.Join(strings.Fields(b.String()), " ")
}
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

var bareURL = regexp.MustCompile(`https?://[^\s<>]+`)

func cleanText(s string) string {
	var render func(shortcode) string
	render = func(sc shortcode) string {
		if !pluginShortcode(sc) {
			return preserveUnknown(sc, render)
		}
		return " " + renderShortcodes(sc.Body, render) + " "
	}
	s = renderShortcodes(s, render)
	return strings.Join(strings.Fields(bareURL.ReplaceAllString(plain(s), "")), " ")
}
func excerpt(i *item) string {
	if strings.TrimSpace(i.Excerpt) != "" {
		return cleanText(i.Excerpt)
	}
	w := strings.Fields(cleanText(i.Content))
	if len(w) > 55 {
		w = w[:55]
	}
	return strings.Join(w, " ")
}

var blockTag = regexp.MustCompile(`(?i)</?(?:address|article|aside|blockquote|div|dl|dt|dd|fieldset|figure|figcaption|form|h[1-6]|header|footer|hr|li|main|nav|ol|p|pre|section|script|style|table|thead|tbody|tr|td|th|ul|video|iframe)(?:\s[^>]*|\s*)>`)
var protectedTag = regexp.MustCompile(`(?is)<(pre|script|style|textarea|svg)\b[^>]*>.*?</(?:pre|script|style|textarea|svg)\s*>`)

// Keep block HTML intact while applying paragraphs and line breaks to prose.
func wpautop(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	var saved []string
	s = protectedTag.ReplaceAllStringFunc(s, func(v string) string {
		saved = append(saved, v)
		token := "SH_PROTECTED_" + fmt.Sprintf("%06d_END", len(saved)-1)
		// svg is often inline (a social icon inside a link). A blank-line
		// placeholder would split that link and the parser would repair it
		// into several anchors.
		if strings.HasPrefix(strings.ToLower(v), "<svg") {
			return token
		}
		return "\n\n" + token + "\n\n"
	})
	s = regexp.MustCompile(`(?i)<br\s*/?>\s*<br\s*/?>`).ReplaceAllString(s, "\n\n")
	s = blockTag.ReplaceAllStringFunc(s, func(v string) string { return "\n\n" + v + "\n\n" })
	chunks := regexp.MustCompile(`\n\s*\n`).Split(strings.TrimSpace(s), -1)
	var b strings.Builder
	stack := []string{}
	for _, c := range chunks {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if blockTag.MatchString(c) {
			b.WriteString(c)
			b.WriteByte('\n')
			tag := strings.ToLower(strings.TrimLeft(c, "</"))
			if n := strings.IndexAny(tag, " >/"); n >= 0 {
				tag = tag[:n]
			}
			if strings.HasPrefix(c, "</") {
				for n := len(stack) - 1; n >= 0; n-- {
					if stack[n] == tag {
						stack = stack[:n]
						break
					}
				}
			} else if tag != "hr" {
				stack = append(stack, tag)
			}

			continue
		}
		if strings.HasPrefix(c, "SH_PROTECTED_") {
			b.WriteString(c)
			continue
		}
		wrap := len(stack) == 0 || stack[len(stack)-1] == "div" || stack[len(stack)-1] == "blockquote" || stack[len(stack)-1] == "section" || stack[len(stack)-1] == "article"
		if wrap {
			b.WriteString("<p>")
		}
		b.WriteString(strings.ReplaceAll(c, "\n", "<br>\n"))
		if wrap {
			b.WriteString("</p>")
		}
		b.WriteByte('\n')
	}
	out := b.String()
	for i, v := range saved {
		out = strings.ReplaceAll(out, "SH_PROTECTED_"+fmt.Sprintf("%06d_END", i), v)
	}
	return strings.TrimSpace(out)
}
func relative(page, target string) string {
	dir := strings.TrimPrefix(path.Dir(filePath(page)), "/")
	if dir == "." {
		dir = ""
	}
	from := strings.Split(dir, "/")
	if dir == "" {
		from = nil
	}
	to := strings.Split(strings.TrimPrefix(target, "/"), "/")
	for len(from) > 0 && len(to) > 0 && from[0] == to[0] {
		from = from[1:]
		to = to[1:]
	}
	s := strings.Repeat("../", len(from)) + strings.Join(to, "/")
	if s == "" {
		return "./"
	}
	return s
}
func filePath(p string) string {
	p = strings.TrimPrefix(p, "/")
	if p == "" || strings.HasSuffix(p, "/") {
		return p + "index.html"
	}
	return p
}
func cleanPath(p string) string {
	u, err := url.PathUnescape(p)
	if err == nil {
		p = u
	}
	p = "/" + strings.TrimLeft(p, "/")
	p = path.Clean(p)
	if p == "." || p == "/" {
		return "/"
	}
	if path.Ext(p) == "" {
		p += "/"
	}
	return p
}
