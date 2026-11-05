package accounts

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

type VerifyCodeRequest struct {
	SSOState string `json:"sso_state"`
	Email    string `json:"email"`
	Code     string `json:"code"`
	Token    string `json:"token"`
	// Nonce accompanies Token: the browser's nonce whose hash the token was
	// issued with.
	Nonce string `json:"nonce"`
	// Name labels the key this sign-in issues (Keys panel). Optional: a link
	// or Google sign-in defaults to "dashboard sign-in", a code to "agent sign-in".
	Name string `json:"name"`
	// Handle is the address a NEW account takes (ignored when the account
	// exists). Refused as invalid_handle, handle_reserved or handle_taken
	// without using up the code, so the caller can try another.
	Handle *string `json:"handle"`
	// ChooseHandle: when this sign-in would create the account, create
	// nothing yet and answer 409 choose_handle with a suggested address; the
	// caller asks the person and verifies again with Handle.
	ChooseHandle bool `json:"choose_handle"`
	// SignupClient: the /mcp consent page sends the client_id of the app
	// being connected, so an account created there is recorded as made for
	// that app (users.signup_source). A label only; ignored for an existing
	// account and for an unknown client.
	SignupClient string `json:"signup_client"`
}

// CodeFailure describes a sign-in refusal without an HTTP response writer.
type CodeFailure struct {
	Error, Code, Reason, Address, SuggestedHandle string
	TriesLeft                                     *int
}
type CodeMessages struct {
	Expired                                                         func(time.Duration) CodeFailure
	Wrong                                                           func(int) CodeFailure
	UsedUp, LinkOtherBrowser, RateLimited, InvalidEmail, SendFailed func() CodeFailure
	Off, Blocked                                                    CodeFailure
}
type HandleActions struct {
	Judge   func(context.Context, string, string) (model.HandleVerdict, error)
	Suggest func(context.Context, string) string
	Address func(string) string
	Assign  func(context.Context, string, string, bool)
}

const maxCodeAttempts = 3

var codeEmailPattern = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func (s *Service) IssueEmailCode(ctx context.Context, mailer CodeSender, allow func(string) bool, address, linkBase string, purpose string, siteID, nonceHash sql.NullString, ip string, geoBlock SignupGeoBlock, authTTL time.Duration, enabled bool, messages CodeMessages, handles HandleActions) (string, int, int, CodeFailure) {
	if !enabled {
		return "", 0, http.StatusForbidden, messages.Off
	}
	address = strings.TrimSpace(strings.ToLower(address))
	if address == "" || !codeEmailPattern.MatchString(address) {
		return "", 0, http.StatusBadRequest, messages.InvalidEmail()
	}

	if _, blocked := geoBlock.BlockedCountry(ip); blocked {
		return "", 0, http.StatusForbidden, messages.Blocked
	}

	// Per-address throttle: stops one email from being mail-bombed even if the
	// attacker rotates source IPs.
	if !allow(emailLimiterKey(address)) {
		return "", 0, http.StatusTooManyRequests, messages.RateLimited()
	}

	code, err := numericCode(6)
	if err != nil {
		log.Printf("auth: generateNumericCode: %v", err)
		return "", 0, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	}
	// Visitor rows still need a random UNIQUE NOT NULL link token.
	// GetAuthTokenByLink refuses non-dashboard rows, so it is inert for the dashboard link flow.
	linkToken, err := secureHex(24)
	if err != nil {
		log.Printf("auth: generateLinkToken: %v", err)
		return "", 0, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	}

	expiresAt := time.Now().Add(authTTL)
	if purpose != "dashboard" {
		nonceHash = sql.NullString{}
	}
	if err := repo.CreateAuthToken(ctx, s.database, address, code, linkToken, expiresAt, purpose, siteID, nonceHash); err != nil {
		log.Printf("auth: CreateAuthToken: %v", err)
		return "", 0, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	}

	link := ""
	if purpose == "dashboard" && linkBase != "" && nonceHash.Valid {
		link = linkBase + "/?token=" + linkToken + "&cn=" + nonceHash.String
	}
	if err := mailer.SendSignInCode(address, code, link); err != nil {
		// Don't expose details to the caller, but log loudly — this is the
		// most likely failure mode in production (Resend misconfig, DNS, etc).
		log.Printf("auth: mailer.SendSignInCode(%s): %v", redactedEmail(address), err)
		return "", 0, http.StatusInternalServerError, messages.SendFailed()
	}

	return address, int(authTTL.Seconds()), 0, CodeFailure{}
}

func (s *Service) VerifyEmailCode(ctx context.Context, allow func(string) bool, req VerifyCodeRequest, purpose string, siteID sql.NullString, randomHandles bool, authTTL time.Duration, enabled bool, messages CodeMessages, handles HandleActions) (model.User, bool, int, CodeFailure) {
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Code = strings.ReplaceAll(strings.TrimSpace(req.Code), "-", "")
	req.Token = strings.TrimSpace(req.Token)

	var tok model.AuthToken
	var err error

	switch {
	case req.Token != "" && purpose == "dashboard":
		tok, err = repo.GetAuthTokenByLink(ctx, s.database, req.Token)
	case req.Email != "" && req.Code != "":
		if !enabled {
			return model.User{}, false, http.StatusForbidden, messages.Off
		}
		tok, err = repo.GetLatestAuthTokenForEmail(ctx, s.database, req.Email, purpose, siteID)
	default:
		return model.User{}, false, http.StatusBadRequest, CodeFailure{Error: "supply token or email+code"}
	}

	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, false, http.StatusUnauthorized, messages.Expired(authTTL)
	}
	if err != nil {
		log.Printf("auth: lookup token: %v", err)
		return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	}

	if tok.Attempts >= maxCodeAttempts {
		return model.User{}, false, http.StatusUnauthorized, messages.UsedUp()
	}

	// A sign-in link works only in the browser that asked for it: the token
	// is redeemed together with the nonce whose hash was stored with it. A
	// link someone else forwards (their own token, to sign a victim into
	// their account) or copies is useless without that browser's nonce.
	if req.Token != "" {
		if !tok.NonceHash.Valid || !nonceMatches(req.Nonce, tok.NonceHash.String) {
			_ = repo.IncrementAuthTokenAttempts(ctx, s.database, tok.ID)
			return model.User{}, false, http.StatusUnauthorized, messages.LinkOtherBrowser()
		}
	}

	// For code-entry path, compare submitted code against the stored value.
	if req.Token == "" {
		// Throttle guesses against a specific address across tokens, on top of
		// the per-token maxCodeAttempts cap.
		if !allow(emailLimiterKey(tok.Email)) {
			return model.User{}, false, http.StatusTooManyRequests, messages.RateLimited()
		}
		if subtle.ConstantTimeCompare([]byte(req.Code), []byte(tok.Code)) != 1 {
			_ = repo.IncrementAuthTokenAttempts(ctx, s.database, tok.ID)
			left := maxCodeAttempts - (tok.Attempts + 1)
			if left <= 0 {
				return model.User{}, false, http.StatusUnauthorized, messages.UsedUp()
			}
			return model.User{}, false, http.StatusUnauthorized, messages.Wrong(left)
		}
	}

	// The code is right. A new account's address: chosen now, before
	// anything is created or the code is spent, so a refusal can be retried.
	var chosen string
	if purpose == "dashboard" && !randomHandles && (req.Handle != nil || req.ChooseHandle) {
		_, lerr := repo.GetUserByUsername(ctx, s.database, tok.Email)
		switch {
		case errors.Is(lerr, sql.ErrNoRows) && req.Handle != nil:
			chosen = strings.ToLower(strings.TrimSpace(*req.Handle))
			v, jerr := handles.Judge(ctx, "", chosen)
			if jerr != nil {
				return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
			}
			if v.Status != 0 {
				return model.User{}, false, v.Status, CodeFailure{Error: v.Error, Code: v.Code, Address: v.Address}
			}
		case errors.Is(lerr, sql.ErrNoRows):
			sug := handles.Suggest(ctx, tok.Email)
			return model.User{}, false, http.StatusConflict, CodeFailure{
				Error: "Choose your address before the account is created: send the same code again with \"handle\". You can change it later, not more than once in 30 days.",
				Code:  "choose_handle", SuggestedHandle: sug, Address: handles.Address(sug),
			}
		case lerr != nil:
			log.Printf("auth: lookup before choosing a handle: %v", lerr)
			return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
		}
	}

	// Spend the token and look the account up in one transaction. A sign-in
	// email change retires the old address's tokens in its own transaction,
	// so the two serialize on the token row: either this verify finds the
	// account under the old address (before the change commits), or the
	// token is already spent. Never an empty new account under the old one.
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	}
	defer tx.Rollback()
	if ok, err := repo.ClaimAuthToken(ctx, tx, tok.ID); err != nil {
		log.Printf("auth: ClaimAuthToken: %v", err)
		return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	} else if !ok {
		return model.User{}, false, http.StatusUnauthorized, messages.Expired(authTTL)
	}
	user, err := repo.GetUserByUsername(ctx, tx, tok.Email)
	if cerr := tx.Commit(); err == nil || errors.Is(err, sql.ErrNoRows) {
		if cerr != nil {
			log.Printf("auth: commit token claim: %v", cerr)
			return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
		}
	}

	// Lazily create the user on first successful verification. requestSignIn no
	// longer pre-creates the row, so this is where a new account is born.
	//
	// This relies on issueEmailCode having already refused every request from
	// a blocked country before any auth_token row could exist (signupgeo.go):
	// a code can only reach here if it was allowed to be issued in the first
	// place. There is no independent geo check at this CreateUser call — if a
	// path other than issueEmailCode is ever added that creates an auth_token,
	// it must apply the same check before doing so.
	created := false
	if errors.Is(err, sql.ErrNoRows) {
		// No key yet: a key is issued only where one is handed out.
		user, err = repo.CreateUser(ctx, s.database, tok.Email, "", false)
		if err != nil {
			if repo.IsUniqueViolation(err) {
				// Concurrent verify won the race — re-fetch the existing row.
				user, err = repo.GetUserByUsername(ctx, s.database, tok.Email)
			}
			if err != nil {
				log.Printf("auth: CreateUser after verify: %v", err)
				return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
			}
		} else {
			created = true
		}
	} else if err != nil {
		log.Printf("auth: GetUserByUsername after verify: %v", err)
		return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	}

	// An existing account under this address that CreatePasswordAccount made
	// (admin-created; a self-signup password account's username is now a
	// synthetic value and could never match tok.Email here in the first
	// place) never proved it controls the address: a code sent to this
	// address proves the opposite, but landing the real owner in that
	// account would hand them a sign-in whoever set that password already
	// knows (pre-account hijacking). Refuse instead of resolving to it; the
	// code is already spent, same as any other refusal past this point.
	if !created {
		if pw, merr := repo.PasswordSignupAccount(ctx, s.database, user.ID); merr != nil {
			log.Printf("auth: signup method lookup: %v", merr)
			return model.User{}, false, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
		} else if pw {
			return model.User{}, false, http.StatusConflict, CodeFailure{Error: "This address already has a password account. Sign in with the password instead.", Code: "password_account_exists"}
		}
	}

	// Assign a URL-safe handle for dashboard users (or lazy-backfill older rows that
	// still have a NULL handle). ClaimHandle only writes WHERE handle IS NULL,
	// so existing handles are never overwritten.
	if purpose == "dashboard" && (created || !user.Handle.Valid) {
		claimed := false
		if created && chosen != "" {
			ok, cerr := repo.ClaimHandle(ctx, s.database, user.ID, chosen)
			if cerr != nil {
				log.Printf("auth: ClaimHandle(chosen %s): %v", chosen, cerr)
			}
			claimed = ok
		}
		if !claimed {
			// Not asked, or taken in the moment since the check: the old rule.
			handles.Assign(ctx, user.ID, tok.Email, randomHandles)
		}
		if refetched, rerr := repo.GetUserByUsername(ctx, s.database, tok.Email); rerr == nil {
			user = refetched
		} else {
			log.Printf("auth: refetch after assignHandle: %v", rerr)
		}
	}

	return user, created, 0, CodeFailure{}
}

func emailLimiterKey(address string) string {
	local, domain, ok := strings.Cut(strings.ToLower(address), "@")
	if !ok {
		return strings.ToLower(address)
	}
	local, _, _ = strings.Cut(local, "+")
	return local + "@" + domain
}

func nonceMatches(nonce, hash string) bool {
	if nonce == "" || len(nonce) > 256 {
		return false
	}
	sum := sha256.Sum256([]byte(nonce))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(hash)) == 1
}
