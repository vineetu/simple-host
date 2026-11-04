package sites

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	rules "github.com/vsriram/simple-host/internal/rules/sites"
)

// PrepareImportLink validates the destination before the optional theme, then
// creates the bearer secret and public record in their original order.
func PrepareImportLink(site, owner string, rawTheme any, ttl time.Duration, checkName bool) (string, ImportLink, error) {
	if checkName {
		if err := rules.ValidateShape(site); err != nil {
			return "", ImportLink{}, err
		}
	}
	var theme string
	if rawTheme != nil {
		value, ok := rawTheme.(string)
		if !ok {
			return "", ImportLink{}, fmt.Errorf("theme_url must be a string, got %T", rawTheme)
		}
		theme = strings.TrimSpace(value)
	}
	var secret [32]byte
	var id [16]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", ImportLink{}, err
	}
	if _, err := rand.Read(id[:]); err != nil {
		return "", ImportLink{}, err
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return base64.RawURLEncoding.EncodeToString(secret[:]), ImportLink{ID: hex.EncodeToString(id[:]), Owner: owner, Site: site, ThemeURL: theme, Expires: time.Now().Add(ttl)}, nil
}
