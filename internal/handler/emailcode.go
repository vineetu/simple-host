package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
)

// EmailLimiter shares the account sign-in throttle with hosted-page sign-in.
func (h *UserHandler) EmailLimiter() *rateLimiter { return h.emailLimiter }

func writeEmailCodeError(w http.ResponseWriter, status int, body errorResponse) {
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "60")
	}
	writeJSON(w, status, body)
}

// issueEmailCode normalizes the address and sends a challenge. An empty linkBase
// sends only the code; dashboard callers supply their public base URL.
func issueEmailCode(ctx context.Context, database *sql.DB, mailer email.Sender, limiter *rateLimiter, address, linkBase string, purpose string, siteID sql.NullString) (string, int, int, errorResponse) {
	address = strings.TrimSpace(strings.ToLower(address))
	if address == "" || !validEmail.MatchString(address) {
		return "", 0, http.StatusBadRequest, errorResponse{Error: "valid email is required"}
	}

	// Per-address throttle: stops one email from being mail-bombed even if the
	// attacker rotates source IPs.
	if !limiter.allow(address) {
		return "", 0, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down"}
	}

	code, err := generateNumericCode(6)
	if err != nil {
		log.Printf("auth: generateNumericCode: %v", err)
		return "", 0, http.StatusInternalServerError, errorResponse{Error: "internal server error"}
	}
	// Visitor rows still need a random UNIQUE NOT NULL link token.
	// GetAuthTokenByLink refuses non-dashboard rows, so it is inert for the dashboard link flow.
	linkToken, err := generateLinkToken(24)
	if err != nil {
		log.Printf("auth: generateLinkToken: %v", err)
		return "", 0, http.StatusInternalServerError, errorResponse{Error: "internal server error"}
	}

	expiresAt := time.Now().Add(authTokenTTL)
	if err := db.CreateAuthToken(ctx, database, address, code, linkToken, expiresAt, purpose, siteID); err != nil {
		log.Printf("auth: CreateAuthToken: %v", err)
		return "", 0, http.StatusInternalServerError, errorResponse{Error: "internal server error"}
	}

	link := ""
	if purpose == "dashboard" && linkBase != "" {
		link = linkBase + "/?token=" + linkToken
	}
	if err := mailer.SendSignInCode(address, code, link); err != nil {
		// Don't expose details to the caller, but log loudly — this is the
		// most likely failure mode in production (Resend misconfig, DNS, etc).
		log.Printf("auth: mailer.SendSignInCode(%s): %v", address, err)
		return "", 0, http.StatusInternalServerError, errorResponse{Error: "could not send verification email"}
	}

	return address, int(authTokenTTL.Seconds()), 0, errorResponse{}
}

// verifyEmailCode shares token verification and lazy account creation. Only the
// dashboard caller accepts a magic-link token in req.
func verifyEmailCode(ctx context.Context, database *sql.DB, limiter *rateLimiter, req verifyRequest, purpose string, siteID sql.NullString) (db.User, bool, int, errorResponse) {
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Code = strings.ReplaceAll(strings.TrimSpace(req.Code), "-", "")
	req.Token = strings.TrimSpace(req.Token)

	var tok db.AuthToken
	var err error

	switch {
	case req.Token != "":
		tok, err = db.GetAuthTokenByLink(ctx, database, req.Token)
	case req.Email != "" && req.Code != "":
		tok, err = db.GetLatestAuthTokenForEmail(ctx, database, req.Email, purpose, siteID)
	default:
		return db.User{}, false, http.StatusBadRequest, errorResponse{Error: "supply token or email+code"}
	}

	if errors.Is(err, sql.ErrNoRows) {
		return db.User{}, false, http.StatusUnauthorized, errorResponse{Error: "invalid or expired code"}
	}
	if err != nil {
		log.Printf("auth: lookup token: %v", err)
		return db.User{}, false, http.StatusInternalServerError, errorResponse{Error: "internal server error"}
	}

	if tok.Attempts >= maxCodeAttempts {
		return db.User{}, false, http.StatusUnauthorized, errorResponse{Error: "too many attempts, request a new code"}
	}

	// For code-entry path, compare submitted code against the stored value.
	if req.Token == "" {
		// Throttle guesses against a specific address across tokens, on top of
		// the per-token maxCodeAttempts cap.
		if !limiter.allow(tok.Email) {
			return db.User{}, false, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down"}
		}
		if subtleConstantTimeEqual(req.Code, tok.Code) != 1 {
			_ = db.IncrementAuthTokenAttempts(ctx, database, tok.ID)
			return db.User{}, false, http.StatusUnauthorized, errorResponse{Error: "invalid or expired code"}
		}
	}

	// Lazily create the user on first successful verification. requestSignIn no
	// longer pre-creates the row, so this is where a new account is born.
	created := false
	user, err := db.GetUserByUsername(ctx, database, tok.Email)
	if errors.Is(err, sql.ErrNoRows) {
		apiKey, kerr := auth.GenerateAPIKey()
		if kerr != nil {
			log.Printf("auth: GenerateAPIKey: %v", kerr)
			return db.User{}, false, http.StatusInternalServerError, errorResponse{Error: "internal server error"}
		}
		user, err = db.CreateUser(ctx, database, tok.Email, apiKey, false)
		if err != nil {
			if isUniqueViolation(err) {
				// Concurrent verify won the race — re-fetch the existing row.
				user, err = db.GetUserByUsername(ctx, database, tok.Email)
			}
			if err != nil {
				log.Printf("auth: CreateUser after verify: %v", err)
				return db.User{}, false, http.StatusInternalServerError, errorResponse{Error: "internal server error"}
			}
		} else {
			created = true
		}
	} else if err != nil {
		log.Printf("auth: GetUserByUsername after verify: %v", err)
		return db.User{}, false, http.StatusInternalServerError, errorResponse{Error: "internal server error"}
	}

	// Assign a URL-safe handle for new users (or lazy-backfill older rows that
	// still have a NULL handle). ClaimHandle only writes WHERE handle IS NULL,
	// so existing handles are never overwritten.
	if created || !user.Handle.Valid {
		assignHandle(ctx, database, user.ID, tok.Email)
		if refetched, rerr := db.GetUserByUsername(ctx, database, tok.Email); rerr == nil {
			user = refetched
		} else {
			log.Printf("auth: refetch after assignHandle: %v", rerr)
		}
	}

	if err := db.MarkAuthTokenUsed(ctx, database, tok.ID); err != nil {
		log.Printf("auth: MarkAuthTokenUsed: %v", err)
	}

	return user, created, 0, errorResponse{}
}
