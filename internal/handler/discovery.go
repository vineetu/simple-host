package handler

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const homepageLinks = `</sitemap.xml>; rel="sitemap", </index.md>; rel="alternate"; type="text/markdown", </.well-known/api-catalog>; rel="api-catalog", </openapi.json>; rel="service-desc"`

// Only explicit markdown and HTML preferences matter here. Equal preferences
// favor markdown; a browser's usual HTML/wildcard Accept still gets HTML.
func acceptsMarkdown(r *http.Request) bool {
	var markdown, htmlQuality float64
	for _, value := range strings.Split(strings.Join(r.Header.Values("Accept"), ","), ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		quality := 1.0
		if q, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(q, 64)
			if err != nil || quality < 0 || quality > 1 {
				continue
			}
		}
		switch media {
		case "text/markdown":
			markdown = max(markdown, quality)
		case "text/html":
			htmlQuality = max(htmlQuality, quality)
		}
	}
	return markdown > 0 && markdown >= htmlQuality
}

// homepageMarkdown reads only the landing's marked copy, excluding the film
// player, scripts, and dashboard. The HTML remains the single source of copy.
// This deliberately supports just the tags used in that section.
func homepageMarkdown(page []byte) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, err
	}
	var content *html.Node
	var find func(*html.Node)
	find = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "id" && a.Val == "homepage-content" {
				content = n
				return
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			find(child)
		}
	}
	find(doc)
	if content == nil {
		return nil, fmt.Errorf("homepage-content missing from landing page")
	}
	var render func(*html.Node) string
	render = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		if n.Type != html.ElementNode || n.Data == "script" || n.Data == "style" {
			return ""
		}
		var b strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			b.WriteString(render(child))
		}
		text := b.String()
		switch n.Data {
		case "h1", "h2", "h3":
			return "\n\n" + strings.Repeat("#", int(n.Data[1]-'0')) + " " + strings.TrimSpace(text) + "\n\n"
		case "p", "div":
			return "\n\n" + strings.TrimSpace(text) + "\n\n"
		case "br":
			return "\n"
		case "a":
			for _, a := range n.Attr {
				if a.Key == "href" {
					return "[" + strings.TrimSpace(text) + "](" + a.Val + ") "
				}
			}
		}
		return text
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(render(content)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" || len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, line)
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func homepageMarkdownHandler() http.HandlerFunc {
	page, err := staticFiles.ReadFile("static/host-story.html")
	if err != nil {
		panic(err)
	}
	body, err := homepageMarkdown(page)
	if err != nil {
		panic(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write(body)
	}
}

// Both discovery files name the configured server, like the served API spec.
func discoveryDocument(name, contentType, publicBaseURL string) http.HandlerFunc {
	body, err := staticFiles.ReadFile("static/.well-known/" + name)
	if err != nil {
		panic(err)
	}
	body = bytes.ReplaceAll(body, []byte("https://simple-host.app"), []byte(strings.TrimRight(publicBaseURL, "/")))
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(body))
	}
}
