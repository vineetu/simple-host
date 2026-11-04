package sites

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

// HandleStore is the account/transaction reader used by handle assignment and availability.
type HandleStore interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func SanitizeHandleBase(email string) string {
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

func AssignHandle(ctx context.Context, q HandleStore, userID, email string, randomHandles bool) {
	if randomHandles {
		// Nobody's handle is an address on the hackathon platform, and an
		// email-derived one could squat an event's name (hack_mode.go).
		for i := 0; i < 5; i++ {
			ok, err := repo.ClaimHandle(ctx, q, userID, randomAccountHandle())
			if err != nil {
				log.Printf("auth: ClaimHandle(%s, hack): %v", userID, err)
				return
			}
			if ok {
				return
			}
		}
		return
	}
	base := SanitizeHandleBase(email)

	// Candidates: base, base-2 … base-20, then base-<first8(userID)>.
	candidates := make([]string, 0, 22)
	if !LabelReservedForNew(base) {
		candidates = append(candidates, base)
	}
	for n := 2; n <= 20; n++ {
		c := base + "-" + strconv.Itoa(n)
		if !LabelReservedForNew(c) {
			candidates = append(candidates, c)
		}
	}
	suffix := userID
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	fallback := base + "-" + suffix
	if !LabelReservedForNew(fallback) {
		candidates = append(candidates, fallback)
	}

	for _, c := range candidates {
		ok, err := repo.ClaimHandle(ctx, q, userID, c)
		if err != nil {
			log.Printf("auth: ClaimHandle(%s, %s): %v", userID, c, err)
			return
		}
		if ok {
			return
		}
	}
	log.Printf("auth: AssignHandle: no free handle for user %s (base=%s)", userID, base)
}

func HandleAddress(handle string) string {
	if d := repo.CurrentPlatformDomain(); d != "" {
		return handle + "." + d
	}
	return handle
}

func HandleTakenMessage(handle string) string {
	return "That address is taken: " + HandleAddress(handle) + " is already in use. Try another."
}

func JudgeHandle(ctx context.Context, q HandleStore, userID, handle string) (HandleVerdict, error) {
	v := HandleVerdict{Address: HandleAddress(handle)}
	if !VisitorHandle.MatchString(handle) || !HandleIsLabel(handle) {
		v.Status, v.Code, v.Error = http.StatusBadRequest, "invalid_handle", handleFormatMsg
		return v, nil
	}
	if LabelReservedForNew(handle) {
		v.Status, v.Code = http.StatusConflict, "handle_reserved"
		v.Error = "That address is reserved: " + v.Address + " can't be used. Try another."
		return v, nil
	}
	taken, err := repo.HandleInUse(ctx, q, userID, handle)
	if err != nil {
		return v, err
	}
	if taken {
		v.Status, v.Code, v.Error = http.StatusConflict, "handle_taken", HandleTakenMessage(handle)
	}
	return v, nil
}

func SuggestHandle(ctx context.Context, q HandleStore, email string) string {
	base := SanitizeHandleBase(email)
	candidates := []string{base}
	for n := 2; n <= 20; n++ {
		candidates = append(candidates, base+"-"+strconv.Itoa(n))
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	candidates = append(candidates, base+"-"+hex.EncodeToString(b[:]))
	for _, c := range candidates {
		if v, err := JudgeHandle(ctx, q, "", c); err == nil && v.Status == 0 {
			return c
		}
	}
	return ""
}

type HandleVerdict = model.HandleVerdict

const handleFormatMsg = "Use 1 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen."

func randomAccountHandle() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return "u-" + hex.EncodeToString(b[:])
}
