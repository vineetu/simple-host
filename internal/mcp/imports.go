package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// ImportLink carries no credential; Mint rechecks the creating connection at use.
type ImportLink struct {
	ID, Owner, Site, ThemeURL string
	Expires                   time.Time
	Mint                      func(context.Context, bool) (string, func(), error)
	reserved                  bool
}

func importOwner(c Caller) string {
	if c.Owner != "" {
		return c.Owner
	}
	return c.APIKey
}

func createImportLink(c *call, args map[string]any) (output, error) {
	site, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	theme, err := optionalString(args, "theme_url")
	if err != nil {
		return output{}, err
	}
	var secret [32]byte
	var id [16]byte
	if _, err = rand.Read(secret[:]); err != nil {
		return output{}, err
	}
	if _, err = rand.Read(id[:]); err != nil {
		return output{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret[:])
	ttl := c.server.cfg.ImportLinkTTL
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	link := ImportLink{ID: hex.EncodeToString(id[:]), Owner: importOwner(c.caller), Site: site, ThemeURL: theme, Expires: time.Now().Add(ttl), Mint: c.caller.MintUpload}
	if link.Mint == nil {
		key := c.caller.APIKey
		link.Mint = func(context.Context, bool) (string, func(), error) { return key, func() {}, nil }
	}
	s := c.server
	s.importMu.Lock()
	defer s.importMu.Unlock()
	count := 0
	for hash, old := range s.imports {
		if !old.Expires.After(time.Now()) && !old.reserved {
			delete(s.imports, hash)
		} else if old.Owner == link.Owner {
			count++
		}
	}
	if count >= 20 {
		return output{}, errors.New("there are already 20 live import links for this account; use one or wait for it to expire")
	}
	if s.cfg.ImportCreated != nil {
		if err := s.cfg.ImportCreated(link); err != nil {
			return output{}, err
		}
	}
	if s.imports == nil {
		s.imports = make(map[[32]byte]ImportLink)
	}
	s.imports[sha256.Sum256([]byte(token))] = link
	out := map[string]any{"import_url": "https://" + s.cfg.APIHost + "/import/" + token, "import_id": link.ID, "expires_at": link.Expires.UTC().Format(time.RFC3339), "site": site, "how": "Give the person this link to open in their browser. They download their own WordPress export: WordPress.com Tools > Export > Export all > Download; optionally Tools > Export > Export media library > Download. Drop those files on the page and press Import. When the page says it finished and they say done, call get_import_report with import_id, switch on each feature using the setup calls it lists, check the pages, and tell them the live address. Never paste the link into a web page."}
	return output{Text: jsonText(out), Structured: out}, nil
}

// ImportLinkForPage never consumes a link. ReserveImport serializes uploads.
func (s *Server) ImportLinkForPage(token string) (ImportLink, bool) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	link, ok := s.imports[sha256.Sum256([]byte(token))]
	return link, ok && link.Expires.After(time.Now())
}
func (s *Server) ReserveImport(token string) (ImportLink, int) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	hash := sha256.Sum256([]byte(token))
	link, ok := s.imports[hash]
	if !ok || !link.Expires.After(time.Now()) {
		return ImportLink{}, 404
	}
	if link.reserved {
		return ImportLink{}, 409
	}
	link.reserved = true
	s.imports[hash] = link
	return link, 0
}
func (s *Server) ReleaseImport(token string, spent bool) {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	hash := sha256.Sum256([]byte(token))
	if spent {
		delete(s.imports, hash)
	} else if link, ok := s.imports[hash]; ok {
		link.reserved = false
		s.imports[hash] = link
	}
}
func getImportReport(c *call, args map[string]any) (output, error) {
	id, err := stringArg(args, "import_id")
	if err != nil {
		return output{}, err
	}
	if c.server.cfg.ImportReport == nil {
		return output{}, errors.New("no import with that id for this account")
	}
	out, err := c.server.cfg.ImportReport(importOwner(c.caller), id)
	return output{Text: jsonText(out), Structured: out}, err
}
