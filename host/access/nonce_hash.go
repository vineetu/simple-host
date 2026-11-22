package access

import (
	"database/sql"

	"regexp"
	"strings"
)

// NonceHashShape is the shape of a SHA-256 digest in unpadded base64url.
var NonceHashShape = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// NonceHashParam validates an optional nonce hash sent by a sign-in page.
func NonceHashParam(s string) (sql.NullString, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{}, true
	}
	if !NonceHashShape.MatchString(s) {
		return sql.NullString{}, false
	}
	return sql.NullString{String: s, Valid: true}, true
}
