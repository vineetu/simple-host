package wpimport

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type term struct {
	ID        string `xml:"term_id"`
	CatID     string `xml:"cat_id"`
	TagID     string `xml:"tag_id"`
	Slug      string `xml:"term_slug"`
	Name      string `xml:"term_name"`
	Parent    string `xml:"term_parent"`
	Taxonomy  string `xml:"term_taxonomy"`
	CatSlug   string `xml:"category_nicename"`
	CatName   string `xml:"cat_name"`
	CatParent string `xml:"category_parent"`
	TagSlug   string `xml:"tag_slug"`
	TagName   string `xml:"tag_name"`
}
type meta struct {
	Key   string `xml:"meta_key"`
	Value string `xml:"meta_value"`
}
type category struct {
	Domain string `xml:"domain,attr"`
	Slug   string `xml:"nicename,attr"`
	Name   string `xml:",chardata"`
}
type comment struct {
	ID       string `xml:"comment_id"`
	Author   string `xml:"comment_author"`
	URL      string `xml:"comment_author_url"`
	Date     string `xml:"comment_date"`
	Content  string `xml:"comment_content"`
	Approved string `xml:"comment_approved"`
	Type     string `xml:"comment_type"`
	Parent   string `xml:"comment_parent"`
}
type item struct {
	Title         string     `xml:"title"`
	GUID          string     `xml:"guid"`
	Link          string     `xml:"link"`
	Date          string     `xml:"post_date"`
	PubDate       string     `xml:"pubDate"`
	Author        string     `xml:"creator"`
	ID            string     `xml:"post_id"`
	Slug          string     `xml:"post_name"`
	Status        string     `xml:"status"`
	Password      string     `xml:"post_password"`
	Parent        string     `xml:"post_parent"`
	Order         int        `xml:"menu_order"`
	Type          string     `xml:"post_type"`
	CommentStatus string     `xml:"comment_status"`
	Attachment    string     `xml:"attachment_url"`
	Content       string     `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Excerpt       string     `xml:"http://wordpress.org/export/1.2/excerpt/ encoded"`
	Excerpt10     string     `xml:"http://wordpress.org/export/1.0/excerpt/ encoded"`
	Excerpt11     string     `xml:"http://wordpress.org/export/1.1/excerpt/ encoded"`
	Meta          []meta     `xml:"postmeta"`
	Categories    []category `xml:"category"`
	Comments      []comment  `xml:"comment"`
	Path          string     `xml:"-"`
}

// public reports whether WordPress shows the item to every visitor: published
// and not behind a post password. Drafts, scheduled, pending, private and
// password-protected items are never written to the site.
func (i *item) public() bool {
	return i.Status == "publish" && strings.TrimSpace(i.Password) == ""
}

func (i *item) metadata(key string) string {
	for _, m := range i.Meta {
		if m.Key == key {
			return m.Value
		}
	}
	return ""
}

type export struct {
	Title, Link, Description, Base, Blog string
	Terms                                []term
	Items                                []*item
}

func parseWXR(ctx context.Context, readers []io.Reader, l Limits) (*export, []string, error) {
	out := &export{}
	var warnings []string
	remaining := l.MaxXMLBytes
	readable := 0
	var lastErr error
	if len(readers) == 0 {
		return nil, nil, fmt.Errorf("no WordPress export supplied")
	}
	for _, r := range readers {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, remaining+1))
		if err != nil {
			return nil, nil, err
		}
		remaining -= int64(len(b))
		if remaining < 0 {
			return nil, nil, fmt.Errorf("WordPress export exceeds XML byte limit")
		}
		b = []byte(strings.ToValidUTF8(string(b), "\ufffd"))
		if err := checkXMLDirectives(b); err != nil {
			if strings.Contains(err.Error(), "DOCTYPE") {
				return nil, nil, err
			}
			lastErr = err
			warnings = append(warnings, "Skipped an unreadable WordPress export: "+err.Error())
			continue
		}
		e, err := decodeWXR(ctx, b, true, l.MaxItems-len(out.Items))
		if err != nil && strings.Contains(err.Error(), "invalid character entity") && !strings.Contains(err.Error(), "DOCTYPE") {
			e, err = decodeWXR(ctx, b, false, l.MaxItems-len(out.Items))
			if err == nil {
				warnings = append(warnings, "Unescaped ampersands required a relaxed XML parse; check the imported content.")
			}
		}
		if err != nil {
			if strings.Contains(err.Error(), "item limit") || ctx.Err() != nil {
				return nil, nil, err
			}
			lastErr = err
			warnings = append(warnings, "Skipped an unreadable WordPress export: "+err.Error())
			continue
		}
		readable++
		if out.Title == "" {
			out.Title = e.Title
		}
		if out.Base == "" {
			out.Base = e.Base
		}
		if out.Link == "" {
			out.Link = e.Link
		}
		if out.Blog == "" {
			out.Blog = e.Blog
		}
		if out.Description == "" {
			out.Description = e.Description
		}
		out.Terms = append(out.Terms, e.Terms...)
		out.Items = append(out.Items, e.Items...)
	}
	if readable == 0 && lastErr != nil {
		return nil, nil, fmt.Errorf("no readable WordPress export: %w", lastErr)
	}
	if out.Base == "" {
		out.Base = out.Link
	}
	return out, warnings, nil
}
func decodeWXR(ctx context.Context, b []byte, strict bool, max int) (*export, error) {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = strict
	e := &export{}
	channel := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := t.(type) {
		case xml.Directive:
			if strings.Contains(strings.ToUpper(string(t)), "ENTITY") {
				return nil, fmt.Errorf("DOCTYPE entity declarations are not allowed")
			}
		case xml.EndElement:
			if t.Name.Local == "channel" {
				channel = false
			}
		case xml.StartElement:
			if t.Name.Local == "channel" {
				channel = true
				continue
			}
			if !channel {
				continue
			}
			switch t.Name.Local {
			case "item":
				if len(e.Items) >= max {
					return nil, fmt.Errorf("WordPress export exceeds item limit")
				}
				var i item
				if err := d.DecodeElement(&i, &t); err != nil {
					return nil, err
				}
				if i.Type == "attachment" && i.Attachment == "" {
					i.Attachment = safeLink(i.GUID)
				}
				if i.Excerpt == "" {
					i.Excerpt = i.Excerpt11
				}
				if i.Excerpt == "" {
					i.Excerpt = i.Excerpt10
				}
				if i.Date == "" {
					i.Date = i.PubDate
				}
				e.Items = append(e.Items, &i)
			case "title", "link", "description", "base_site_url", "base_blog_url":
				var v string
				if err := d.DecodeElement(&v, &t); err != nil {
					return nil, err
				}
				switch t.Name.Local {
				case "title":
					e.Title = v
				case "link":
					e.Link = v
				case "description":
					e.Description = v
				case "base_site_url":
					e.Base = v
				case "base_blog_url":
					e.Blog = v
				}
			case "category", "tag", "term":
				var v term
				if err := d.DecodeElement(&v, &t); err != nil {
					return nil, err
				}
				if t.Name.Local == "category" {
					if v.ID == "" {
						v.ID = v.CatID
					}
					v.Taxonomy = "category"
					v.Slug = v.CatSlug
					v.Name = v.CatName
					v.Parent = v.CatParent
				}
				if t.Name.Local == "tag" {
					if v.ID == "" {
						v.ID = v.TagID
					}
					v.Taxonomy = "post_tag"
					v.Slug = v.TagSlug
					v.Name = v.TagName
				}
				e.Terms = append(e.Terms, v)
			}
		}
	}
	return e, nil
}

// RawToken inspects directives even inside elements DecodeElement would skip.
func checkXMLDirectives(b []byte) error {
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	for {
		token, err := d.RawToken()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// WordPress exports never carry a DOCTYPE, so refuse any, not just
		// ones that declare entities.
		if directive, ok := token.(xml.Directive); ok && strings.HasPrefix(strings.ToUpper(strings.TrimSpace(string(directive))), "DOCTYPE") {
			return fmt.Errorf("this file has a DOCTYPE, which WordPress exports never have; upload the export WordPress gave you")
		}
	}
}
