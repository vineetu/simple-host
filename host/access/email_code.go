package access

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
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
// A refusal the route words itself names its Refusal (Wrong with TriesLeft);
// any other carries its text here.
type CodeFailure struct {
	Refusal                                       CodeRefusal
	Error, Code, Reason, Address, SuggestedHandle string
	TriesLeft                                     *int
}

// CodeRefusal is a sign-in refusal whose words the route supplies.
type CodeRefusal int

const (
	CodeWorded           CodeRefusal = iota // the text is in the CodeFailure
	CodeExpired                             // the code or link is past its time
	CodeWrong                               // the code is not right; TriesLeft remain
	CodeUsedUp                              // the code was used or its tries are spent
	CodeLinkOtherBrowser                    // the link was opened in another browser
	CodeRateLimited
	CodeInvalidEmail
	CodeSendFailed
	CodeSignInOff // EMAIL_SIGNIN=off
	CodeBlocked   // sign-up refused from this country
)

const maxCodeAttempts = 3

// IssueEmailCode normalizes the address and sends a challenge. The email
// carries a sign-in link only for a dashboard request that sent a nonce hash
// (the link then works only in the browser holding that nonce); otherwise it
// carries the code alone.
//
// ip and geoBlock gate every request, new address or existing: when the
// request's country is on SIGNUP_BLOCKED_COUNTRIES, no code is ever sent and
// no auth_token row is created — there is no "does this address already have
// an account" check first (deliberately: that lookup is itself the thing a
// probe could use to enumerate addresses, and the owner decided a blanket
// refusal is simpler and sufficient). geoBlock's zero value (the default,
// feature off) never looks anything up, so this is a no-op for every caller
// until the knob is set.
func (s *Service) IssueEmailCode(ctx context.Context, mailer CodeSender, allow func(string) bool, address, linkBase string, purpose string, siteID, nonceHash sql.NullString, ip string, geoBlock SignupGeoBlock, authTTL time.Duration, enabled bool) (string, int, int, CodeFailure) {
	if !enabled {
		return "", 0, http.StatusForbidden, CodeFailure{Refusal: CodeSignInOff}
	}
	address, ok := authrules.NormalEmail(address)
	if !ok {
		return "", 0, http.StatusBadRequest, CodeFailure{Refusal: CodeInvalidEmail}
	}

	if _, blocked := geoBlock.BlockedCountry(ip); blocked {
		return "", 0, http.StatusForbidden, CodeFailure{Refusal: CodeBlocked}
	}

	// Per-address throttle: stops one email from being mail-bombed even if the
	// attacker rotates source IPs.
	if !allow(model.BaseEmail(address)) {
		return "", 0, http.StatusTooManyRequests, CodeFailure{Refusal: CodeRateLimited}
	}

	code, err := NumericCode(6)
	if err != nil {
		log.Printf("auth: generateNumericCode: %v", err)
		return "", 0, http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
	}
	// Visitor rows still need a random UNIQUE NOT NULL link token.
	// GetAuthTokenByLink refuses non-dashboard rows, so it is inert for the dashboard link flow.
	linkToken, err := SecureHex(24)
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
		log.Printf("auth: mailer.SendSignInCode(%s): %v", RedactedEmail(address), err)
		return "", 0, http.StatusInternalServerError, CodeFailure{Refusal: CodeSendFailed}
	}

	return address, int(authTTL.Seconds()), 0, CodeFailure{}
}

// VerifyEmailCode redeems an emailed code (or a dashboard sign-in link) and
// returns the account it signs in, creating it on first use. A refusal is its
// HTTP status and body; status 0 is success. The steps run in this order, so
// every refusal is decided before the next step can change anything.
func (s *Service) VerifyEmailCode(ctx context.Context, allow func(string) bool, req VerifyCodeRequest, purpose string, siteID sql.NullString, randomHandles bool, authTTL time.Duration, enabled bool) (model.User, bool, int, CodeFailure) {
	req = normalizeVerifyRequest(req)
	tok, status, fail := s.lookupCodeToken(ctx, req, purpose, siteID, authTTL, enabled)
	if status != 0 {
		return model.User{}, false, status, fail
	}
	if status, fail := s.checkCodeToken(ctx, tok, req, allow); status != 0 {
		return model.User{}, false, status, fail
	}
	chosen, status, fail := s.chooseNewHandle(ctx, tok, req, purpose, randomHandles)
	if status != 0 {
		return model.User{}, false, status, fail
	}
	user, created, status, fail := s.spendCodeToken(ctx, tok, authTTL)
	if status != 0 {
		return model.User{}, false, status, fail
	}
	if !created {
		if status, fail := s.refusePasswordAccount(ctx, user.ID); status != 0 {
			return model.User{}, false, status, fail
		}
	}
	if purpose == "dashboard" && (created || !user.Handle.Valid) {
		user = s.assignCodeHandle(ctx, user, tok.Email, chosen, created, randomHandles)
	}
	return user, created, 0, CodeFailure{}
}

// internalCodeFailure is the generic 500 of a failed verification step.
func internalCodeFailure() (int, CodeFailure) {
	return http.StatusInternalServerError, CodeFailure{Error: "internal server error"}
}

func normalizeVerifyRequest(req VerifyCodeRequest) VerifyCodeRequest {
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Code = strings.ReplaceAll(strings.TrimSpace(req.Code), "-", "")
	req.Token = strings.TrimSpace(req.Token)
	return req
}

// lookupCodeToken finds the token the request redeems: a dashboard link by
// its token, else the address's latest code for this purpose and site.
func (s *Service) lookupCodeToken(ctx context.Context, req VerifyCodeRequest, purpose string, siteID sql.NullString, authTTL time.Duration, enabled bool) (model.AuthToken, int, CodeFailure) {
	var tok model.AuthToken
	var err error
	switch {
	case req.Token != "" && purpose == "dashboard":
		tok, err = repo.GetAuthTokenByLink(ctx, s.database, req.Token)
	case req.Email != "" && req.Code != "":
		if !enabled {
			return tok, http.StatusForbidden, CodeFailure{Refusal: CodeSignInOff}
		}
		tok, err = repo.GetLatestAuthTokenForEmail(ctx, s.database, req.Email, purpose, siteID)
	default:
		return tok, http.StatusBadRequest, CodeFailure{Error: "supply token or email+code"}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return tok, http.StatusUnauthorized, CodeFailure{Refusal: CodeExpired}
	}
	if err != nil {
		log.Printf("auth: lookup token: %v", err)
		status, fail := internalCodeFailure()
		return tok, status, fail
	}
	return tok, 0, CodeFailure{}
}

// checkCodeToken refuses a token whose tries are used up, a link opened in
// another browser than the one that asked for it, and a wrong code. Every
// wrong answer spends a try.
func (s *Service) checkCodeToken(ctx context.Context, tok model.AuthToken, req VerifyCodeRequest, allow func(string) bool) (int, CodeFailure) {
	if tok.Attempts >= maxCodeAttempts {
		return http.StatusUnauthorized, CodeFailure{Refusal: CodeUsedUp}
	}
	// A sign-in link works only in the browser that asked for it: the token
	// is redeemed together with the nonce whose hash was stored with it. A
	// link someone else forwards (their own token, to sign a victim into
	// their account) or copies is useless without that browser's nonce.
	if req.Token != "" {
		if !tok.NonceHash.Valid || !nonceMatches(req.Nonce, tok.NonceHash.String) {
			_ = repo.IncrementAuthTokenAttempts(ctx, s.database, tok.ID)
			return http.StatusUnauthorized, CodeFailure{Refusal: CodeLinkOtherBrowser}
		}
		return 0, CodeFailure{}
	}
	// Throttle guesses against a specific address across tokens, on top of
	// the per-token maxCodeAttempts cap.
	if !allow(model.BaseEmail(tok.Email)) {
		return http.StatusTooManyRequests, CodeFailure{Refusal: CodeRateLimited}
	}
	if subtle.ConstantTimeCompare([]byte(req.Code), []byte(tok.Code)) != 1 {
		_ = repo.IncrementAuthTokenAttempts(ctx, s.database, tok.ID)
		left := maxCodeAttempts - (tok.Attempts + 1)
		if left <= 0 {
			return http.StatusUnauthorized, CodeFailure{Refusal: CodeUsedUp}
		}
		return http.StatusUnauthorized, CodeFailure{Refusal: CodeWrong, TriesLeft: &left}
	}
	return 0, CodeFailure{}
}

// chooseNewHandle is the address a new account asked for, judged now, before
// anything is created or the code is spent, so a refusal can be retried. A
// new account that has not chosen yet is asked to (choose_handle).
func (s *Service) chooseNewHandle(ctx context.Context, tok model.AuthToken, req VerifyCodeRequest, purpose string, randomHandles bool) (string, int, CodeFailure) {
	if purpose != "dashboard" || randomHandles || (req.Handle == nil && !req.ChooseHandle) {
		return "", 0, CodeFailure{}
	}
	_, lerr := repo.GetUserByUsername(ctx, s.database, tok.Email)
	switch {
	case errors.Is(lerr, sql.ErrNoRows) && req.Handle != nil:
		chosen := strings.ToLower(strings.TrimSpace(*req.Handle))
		v, jerr := JudgeHandle(ctx, s.database, "", chosen)
		if jerr != nil {
			status, fail := internalCodeFailure()
			return "", status, fail
		}
		if v.Status != 0 {
			return "", v.Status, CodeFailure{Error: v.Error, Code: v.Code, Address: v.Address}
		}
		return chosen, 0, CodeFailure{}
	case errors.Is(lerr, sql.ErrNoRows):
		sug := SuggestHandle(ctx, s.database, tok.Email)
		return "", http.StatusConflict, CodeFailure{
			Error: "Choose your address before the account is created: send the same code again with \"handle\". You can change it later, not more than once in 30 days.",
			Code:  "choose_handle", SuggestedHandle: sug, Address: HandleAddress(sug),
		}
	case lerr != nil:
		log.Printf("auth: lookup before choosing a handle: %v", lerr)
		status, fail := internalCodeFailure()
		return "", status, fail
	}
	return "", 0, CodeFailure{}
}

// spendCodeToken spends the token and returns the account under its address,
// creating it on first use.
//
// The token is spent and the account looked up in one transaction. A sign-in
// email change retires the old address's tokens in its own transaction, so
// the two serialize on the token row: either this verify finds the account
// under the old address (before the change commits), or the token is already
// spent. Never an empty new account under the old one.
func (s *Service) spendCodeToken(ctx context.Context, tok model.AuthToken, authTTL time.Duration) (model.User, bool, int, CodeFailure) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		status, fail := internalCodeFailure()
		return model.User{}, false, status, fail
	}
	defer tx.Rollback()
	if ok, err := repo.ClaimAuthToken(ctx, tx, tok.ID); err != nil {
		log.Printf("auth: ClaimAuthToken: %v", err)
		status, fail := internalCodeFailure()
		return model.User{}, false, status, fail
	} else if !ok {
		return model.User{}, false, http.StatusUnauthorized, CodeFailure{Refusal: CodeExpired}
	}
	user, err := repo.GetUserByUsername(ctx, tx, tok.Email)
	if cerr := tx.Commit(); err == nil || errors.Is(err, sql.ErrNoRows) {
		if cerr != nil {
			log.Printf("auth: commit token claim: %v", cerr)
			status, fail := internalCodeFailure()
			return model.User{}, false, status, fail
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return s.createCodeAccount(ctx, tok.Email)
	}
	if err != nil {
		log.Printf("auth: GetUserByUsername after verify: %v", err)
		status, fail := internalCodeFailure()
		return model.User{}, false, status, fail
	}
	return user, false, 0, CodeFailure{}
}

// createCodeAccount creates the account on its first successful
// verification: requestSignIn no longer pre-creates the row, so this is where
// a new account is born. It has no key yet: a key is issued only where one is
// handed out.
//
// This relies on issueEmailCode having already refused every request from a
// blocked country before any auth_token row could exist (signupgeo.go): a
// code can only reach here if it was allowed to be issued in the first place.
// There is no independent geo check at this CreateUser call — if a path other
// than issueEmailCode is ever added that creates an auth_token, it must apply
// the same check before doing so.
func (s *Service) createCodeAccount(ctx context.Context, address string) (model.User, bool, int, CodeFailure) {
	user, err := repo.CreateUser(ctx, s.database, address, "", false)
	if err == nil {
		return user, true, 0, CodeFailure{}
	}
	if repo.IsUniqueViolation(err) {
		// Concurrent verify won the race — re-fetch the existing row.
		user, err = repo.GetUserByUsername(ctx, s.database, address)
	}
	if err != nil {
		log.Printf("auth: CreateUser after verify: %v", err)
		status, fail := internalCodeFailure()
		return model.User{}, false, status, fail
	}
	return user, false, 0, CodeFailure{}
}

// refusePasswordAccount refuses an existing account under this address that
// CreatePasswordAccount made (admin-created; a self-signup password account's
// username is now a synthetic value and could never match the address here in
// the first place): it never proved it controls the address. A code sent to
// this address proves the opposite, but landing the real owner in that
// account would hand them a sign-in whoever set that password already knows
// (pre-account hijacking). The code is already spent, same as any other
// refusal past this point.
func (s *Service) refusePasswordAccount(ctx context.Context, userID string) (int, CodeFailure) {
	pw, err := repo.PasswordSignupAccount(ctx, s.database, userID)
	if err != nil {
		log.Printf("auth: signup method lookup: %v", err)
		return internalCodeFailure()
	}
	if pw {
		return http.StatusConflict, CodeFailure{Error: "This address already has a password account. Sign in with the password instead.", Code: "password_account_exists"}
	}
	return 0, CodeFailure{}
}

// assignCodeHandle gives a dashboard account its URL-safe handle (or
// lazy-backfills an older row that still has none): the one it chose when
// still free, else the old rule. ClaimHandle only writes WHERE handle IS
// NULL, so existing handles are never overwritten.
func (s *Service) assignCodeHandle(ctx context.Context, user model.User, address, chosen string, created, randomHandles bool) model.User {
	claimed := false
	if created && chosen != "" {
		ok, err := repo.ClaimHandle(ctx, s.database, user.ID, chosen)
		if err != nil {
			log.Printf("auth: ClaimHandle(chosen %s): %v", chosen, err)
		}
		claimed = ok
	}
	if !claimed {
		// Not asked, or taken in the moment since the check: the old rule.
		AssignHandle(ctx, s.database, user.ID, address, randomHandles)
	}
	refetched, err := repo.GetUserByUsername(ctx, s.database, address)
	if err != nil {
		log.Printf("auth: refetch after assignHandle: %v", err)
		return user
	}
	return refetched
}

func nonceMatches(nonce, hash string) bool {
	if nonce == "" || len(nonce) > 256 {
		return false
	}
	sum := sha256.Sum256([]byte(nonce))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(hash)) == 1
}
