package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

// EmailChangeError describes the existing refusal of an email change.
type EmailChangeError struct {
	Status        int
	Code, Message string
}

func (e *EmailChangeError) Error() string { return e.Message }
func emailChangeFailure(status int, code, message string) error {
	return &EmailChangeError{Status: status, Code: code, Message: message}
}

type EmailChangeResult struct{ OldEmail, NewEmail, UndoToken string }

// VerifyEmailChange holds the account row before the pending change, and keeps
// wrong-attempt accounting and address replacement in their original transactions.
func (s *Service) VerifyEmailChange(ctx context.Context, user *model.User, code, oldCode, reviewer string, allow func(string) bool, undoTTL time.Duration) (EmailChangeResult, error) {
	zero := EmailChangeResult{}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	oldEmail, err := repo.LockSignInEmail(ctx, tx, user.ID)
	if err != nil {
		return zero, err
	}
	change, err := repo.GetEmailChangeForUpdate(ctx, tx, user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return zero, emailChangeFailure(400, "no_pending_change", "no email change is waiting, or its code expired; ask for a new code")
	}
	if err != nil {
		return zero, err
	}
	if change.Attempts >= 3 {
		return zero, emailChangeFailure(401, "invalid_code", "too many attempts, request a new code")
	}
	if !allow(change.NewEmail) {
		return zero, emailChangeFailure(429, "", "rate limit exceeded, slow down")
	}
	newOK := subtle.ConstantTimeCompare([]byte(hashChangeCode(code)), []byte(change.CodeHash)) == 1
	oldOK := change.OldCodeHash != "" && subtle.ConstantTimeCompare([]byte(hashChangeCode(oldCode)), []byte(change.OldCodeHash)) == 1
	if !newOK || !oldOK {
		if err := repo.CountEmailChangeAttempt(ctx, tx, user.ID); err == nil {
			_ = tx.Commit()
		}
		return zero, emailChangeFailure(401, "invalid_code", "invalid or expired code")
	}
	if reviewer != "" && strings.EqualFold(change.NewEmail, reviewer) {
		return zero, emailChangeFailure(400, "reserved_email", "that address cannot be used to sign in")
	}
	taken, err := repo.EmailInUseByOther(ctx, tx, user.ID, change.NewEmail)
	if err != nil {
		return zero, err
	}
	if taken {
		return zero, emailChangeFailure(409, "email_taken", "another Simple Host account already signs in with that address")
	}
	undo, err := secureHex(24)
	if err != nil {
		return zero, err
	}
	err = repo.ApplyEmailChange(ctx, tx, user.ID, oldEmail, change.NewEmail, user.KeyHash, hashChangeCode(undo), undoTTL)
	if err == nil {
		err = tx.Commit()
	}
	if repo.IsUniqueViolation(err) {
		return zero, emailChangeFailure(409, "email_taken", "another Simple Host account already signs in with that address")
	}
	if err != nil {
		log.Printf("email change user_id=%s: apply: %v", user.ID, err)
		return zero, err
	}
	return EmailChangeResult{OldEmail: oldEmail, NewEmail: change.NewEmail, UndoToken: undo}, nil
}
func hashChangeCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}
func secureHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *Service) PutEmailChange(ctx context.Context, userID, address, code, oldCode string, expires time.Time) error {
	return repo.PutEmailChange(ctx, s.database, userID, address, hashChangeCode(code), hashChangeCode(oldCode), expires)
}
func (s *Service) EmailChangeTarget(ctx context.Context, userID string) (model.SignInAlertTarget, error) {
	return repo.GetSignInAlertTarget(ctx, s.database, userID)
}
func (s *Service) GetEmailChangeUndo(ctx context.Context, hash string) (model.EmailChangeUndo, error) {
	return repo.GetEmailChangeUndo(ctx, s.database, hash)
}
func (s *Service) UndoEmailChange(ctx context.Context, hash string) (model.EmailChangeUndo, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return model.EmailChangeUndo{}, err
	}
	defer tx.Rollback()
	result, err := repo.UndoEmailChangeInTx(ctx, tx, hash)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (s *Service) ListSignInIdentities(ctx context.Context, userID string) ([]model.SignInIdentity, error) {
	return repo.ListSignInIdentities(ctx, s.database, userID)
}
func (s *Service) UnlinkSignInIdentity(ctx context.Context, userID, id string) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := repo.UnlinkSignInIdentityInTx(ctx, tx, userID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RequestEmailChange records both inbox challenges before sending either message.
func (s *Service) RequestEmailChange(ctx context.Context, userID, address, current string, mailer CodeSender, ttl time.Duration, supportText func(string) string) error {
	code, err := numericCode(6)
	if err != nil {
		return err
	}
	oldCode, err := numericCode(6)
	if err != nil {
		return err
	}
	if err := s.PutEmailChange(ctx, userID, address, code, oldCode, time.Now().Add(ttl)); err != nil {
		log.Printf("email change user_id=%s: store: %v", userID, err)
		return err
	}
	if err := SendEmailChangeCode(mailer, address, code, ttl); err != nil {
		log.Printf("email change: send code to %s: %v", redactedEmail(address), err)
		return emailChangeFailure(500, "", "could not send verification email")
	}
	if err := SendEmailChangeCurrentCode(mailer, current, address, oldCode, ttl, supportText); err != nil {
		log.Printf("email change: send code to current %s: %v", redactedEmail(current), err)
		return emailChangeFailure(500, "", "could not send verification email")
	}
	return nil
}
func numericCode(digits int) (string, error) {
	max := big.NewInt(1)
	for i := 0; i < digits; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	s := n.String()
	for len(s) < digits {
		s = "0" + s
	}
	return s, nil
}
func redactedEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return "***"
	}
	return string([]rune(local)[0]) + "***@" + domain
}
