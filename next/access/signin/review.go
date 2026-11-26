package signin

// Reviewer sign-in: one account, named by SH_REVIEW_EMAIL, may sign in with
// a password, so a plugin directory's reviewer can connect without an
// emailed code (the directories require demo credentials that need none).
// It is off unless SH_REVIEW_EMAIL and SH_REVIEW_PASSWORD_HASH are both set,
// opens only that account, never an operator, and the sign-in page shows the
// form only on the way to approving an AI app (/oauth/authorize).
//
// The hash is the old service's REVIEW_ACCOUNT_PASSWORD_HASH format, so its
// value carries over: pbkdf2-sha256$<iterations>$<salt>$<key>, base64url,
// at least 600,000 iterations. `simple-host-next review-hash` makes one.

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"simplehost/app"
	"simplehost/core/limits"
	"simplehost/core/web"
)

const (
	reviewScheme    = "pbkdf2-sha256"
	reviewMinRounds = 600_000
	reviewKeyBytes  = 32
	// ReviewMinPassword: the account is on the internet behind a password alone.
	ReviewMinPassword = 16
)

// HashReviewPassword is the SH_REVIEW_PASSWORD_HASH value for a password.
func HashReviewPassword(password string) (string, error) {
	if len([]rune(password)) < ReviewMinPassword {
		return "", fmt.Errorf("the password must be at least %d characters", ReviewMinPassword)
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	key, err := pbkdf2.Key(sha256.New, password, salt, reviewMinRounds, reviewKeyBytes)
	if err != nil {
		return "", err
	}
	e := base64.RawURLEncoding
	return strings.Join([]string{reviewScheme, strconv.Itoa(reviewMinRounds), e.EncodeToString(salt), e.EncodeToString(key)}, "$"), nil
}

type reviewHash struct {
	rounds    int
	salt, key []byte
}

func parseReviewHash(s string) (reviewHash, error) {
	parts := strings.Split(strings.TrimSpace(s), "$")
	if len(parts) != 4 || parts[0] != reviewScheme {
		return reviewHash{}, errors.New("not a pbkdf2-sha256$<iterations>$<salt>$<key> hash")
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < reviewMinRounds || rounds > 50_000_000 {
		return reviewHash{}, errors.New("iterations out of range")
	}
	e := base64.RawURLEncoding
	salt, err1 := e.DecodeString(parts[2])
	key, err2 := e.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(salt) < 16 || len(key) != reviewKeyBytes {
		return reviewHash{}, errors.New("salt or key malformed")
	}
	return reviewHash{rounds, salt, key}, nil
}

func (h reviewHash) matches(password string) bool {
	got, err := pbkdf2.Key(sha256.New, password, h.salt, h.rounds, reviewKeyBytes)
	return err == nil && subtle.ConstantTimeCompare(got, h.key) == 1
}

func review(cfg app.Config) (string, reviewHash, error) {
	email := strings.ToLower(strings.TrimSpace(cfg.ReviewEmail))
	if email == "" || cfg.ReviewPasswordHash == "" {
		return "", reviewHash{}, errors.New("off")
	}
	h, err := parseReviewHash(cfg.ReviewPasswordHash)
	return email, h, err
}

var errReview = web.Refuse(http.StatusUnauthorized, "bad_password", "That email and password do not match the reviewer account.")

// reviewer is POST /v1/auth/reviewer {email, password}: signs this browser
// in as the reviewer account, as a verified code does.
func (h *handler) reviewer(w http.ResponseWriter, r *http.Request) error {
	email, hash, err := review(h.a.Config)
	if err != nil {
		return web.NotFound
	}
	// Per address, and for everyone together: a spread-out guessing run is
	// as slow as one, and so is the CPU each derivation costs.
	if err := limits.Allow(r, limits.ReviewTries, "ip:"+limits.Address(r)); err != nil {
		return err
	}
	if err := limits.Allow(r, limits.ReviewTries, "reviewer"); err != nil {
		return err
	}
	var in struct{ Email, Password string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	// Derive before comparing the address, so both mistakes cost the same.
	passOK := len(in.Password) <= 1024 && hash.matches(in.Password)
	mailOK := subtle.ConstantTimeCompare([]byte(strings.ToLower(strings.TrimSpace(in.Email))), []byte(email)) == 1
	if !passOK || !mailOK {
		return errReview
	}
	who, err := h.a.People.Ensure(r.Context(), email)
	if err != nil {
		return err
	}
	if who.Operator || who.IsTeam {
		return web.Refuse(http.StatusForbidden, "not_available", "The reviewer account is not available.")
	}
	if who.Suspended {
		return web.Refuse(http.StatusForbidden, "account_suspended", "This account is suspended.")
	}
	if err := h.a.People.StartSession(w, r, who.ID); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]string{"handle": who.Handle})
	return nil
}
