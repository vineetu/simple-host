package handler

import (
	"context"
	"log"
	"strconv"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// reservedHandles are path segments / product names that must never be claimed
// as user handles (they collide with API or site routes).
var reservedHandles = map[string]bool{
	"v1":          true,
	"internal":    true,
	"api":         true,
	"auth":        true,
	"me":          true,
	"sites":       true,
	"handles":     true,
	"by-id":       true,
	"static":      true,
	"assets":      true,
	"well-known":  true,
	"favicon.ico": true,
	"robots.txt":  true,
	"admin":       true,
	"root":        true,
	"www":         true,
	"app":         true,
	"dashboard":   true,
	"login":       true,
	"logout":      true,
	"health":      true,
	"healthz":     true,
	"readyz":      true,
	"skills":      true,
	"plugin":      true,
	"install":     true,
	"openapi":     true,
	"llms.txt":    true,
}

// sanitizeHandleBase turns an email into a URL-safe handle base from its local-part:
// lowercase, non-[a-z0-9-] → '-', collapse dashes, trim, truncate to 30 chars.
// Empty result becomes "user".
func sanitizeHandleBase(email string) string {
	local := email
	if i := strings.IndexByte(email, '@'); i >= 0 {
		local = email[:i]
	}
	local = strings.ToLower(local)

	var b strings.Builder
	b.Grow(len(local))
	prevDash := false
	for _, r := range local {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		if r == '-' {
			if prevDash {
				continue
			}
			prevDash = true
			b.WriteByte('-')
			continue
		}
		prevDash = false
		b.WriteRune(r)
	}

	s := strings.Trim(b.String(), "-")
	if len(s) > 30 {
		s = strings.TrimRight(s[:30], "-")
	}
	if s == "" {
		return "user"
	}
	return s
}

// assignHandle claims a free URL-safe handle for userID derived from email.
// Failures are logged but never fatal — a missing handle must not break sign-in.
func (h *UserHandler) assignHandle(ctx context.Context, userID, email string) {
	base := sanitizeHandleBase(email)

	// Candidates: base, base-2 … base-20, then base-<first8(userID)>.
	candidates := make([]string, 0, 22)
	if !reservedHandles[base] {
		candidates = append(candidates, base)
	}
	for n := 2; n <= 20; n++ {
		c := base + "-" + strconv.Itoa(n)
		if !reservedHandles[c] {
			candidates = append(candidates, c)
		}
	}
	suffix := userID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	fallback := base + "-" + suffix
	if !reservedHandles[fallback] {
		candidates = append(candidates, fallback)
	}

	for _, c := range candidates {
		ok, err := db.ClaimHandle(ctx, h.database, userID, c)
		if err != nil {
			log.Printf("auth: ClaimHandle(%s, %s): %v", userID, c, err)
			return
		}
		if ok {
			return
		}
	}
	log.Printf("auth: assignHandle: no free handle for user %s (base=%s)", userID, base)
}
