package legacy

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	storagerules "github.com/vsriram/simple-host/internal/rules/storage"
)

// ListMineInput identifies a visitor and the requested page of their entries.
type ListMineInput struct {
	SiteID, Name string
	Access       VisitorAccessInput
	DeclareFirst bool
	Limit        int
	Before       int64
}

// ListMine checks visitor access and returns their entries for a list name.
func (a *Adapter) ListMine(ctx context.Context, in ListMineInput) ([]db.CollectionItem, db.DataSettings, error) {
	if err := a.CheckVisitorAccess(ctx, in.Access); err != nil {
		return nil, db.DataSettings{}, err
	}
	set, err := a.WriteCollectionSettings(ctx, in.SiteID, in.Name, in.DeclareFirst)
	if err != nil {
		return nil, set, err
	}
	if !storagerules.LegacyEntriesLike(set.Kind, set.Shared) && set.Kind != db.KindBoard {
		return nil, set, &Error{Code: "wrong_kind"}
	}
	items, err := repo.ListOwnEntries(ctx, a.database, in.SiteID, in.Name, in.Access.UserID, in.Limit, in.Before)
	return items, set, err
}

// VisitorAccessInput contains the session and request facts for a visitor write.
type VisitorAccessInput struct {
	UserID              string
	SessionID           []byte
	NeedCSRF, CSRFValid bool
	Idle                time.Duration
}

// CheckVisitorAccess checks suspension and CSRF before touching the session.
func (a *Adapter) CheckVisitorAccess(ctx context.Context, in VisitorAccessInput) error {
	suspended, err := db.UserSuspended(ctx, a.database, in.UserID)
	if err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if suspended {
		return &Error{Code: "account_suspended"}
	}
	if in.NeedCSRF && !in.CSRFValid {
		return &Error{Code: "csrf_required"}
	}
	_ = db.TouchVisitorSession(ctx, a.database, in.SessionID, in.Idle)
	return nil
}

// SaverInput identifies a site and the email to check.
type SaverInput struct {
	SiteID, Email string
}

// SaverAllowed checks whether an email may save on a site.
func (a *Adapter) SaverAllowed(ctx context.Context, in SaverInput) (bool, error) {
	return repo.SaverAllowed(ctx, a.database, in.SiteID, in.Email)
}

// PersonSaverInput identifies a visitor and whether an internal pass is used.
type PersonSaverInput struct {
	SiteID, UserID string
	VisitorPass    bool
}

// PersonSaverAllowed checks a visitor against the site's saver rules.
func (a *Adapter) PersonSaverAllowed(ctx context.Context, in PersonSaverInput) (bool, error) {
	email := ""
	if in.UserID != "" {
		var err error
		email, err = a.visitorEmail(ctx, in.UserID)
		if err != nil {
			return false, err
		}
		if email == "" {
			return false, &Error{Code: "missing_email"}
		}
	}
	ok, err := repo.SaverAllowed(ctx, a.database, in.SiteID, email)
	if err != nil || ok {
		return ok, err
	}
	if in.UserID != "" && !in.VisitorPass {
		if ownerID, _, err := db.GetSiteWriteGate(ctx, a.database, in.SiteID); err == nil && ownerID == in.UserID {
			return true, nil
		}
	}
	return false, nil
}

// UpdateEntryInput contains the validated patch for a visitor's entry.
type UpdateEntryInput struct {
	SiteID, Name                       string
	VisitorAccess                      VisitorAccessInput
	ID                                 int64
	ValidID, DeclareFirst, VisitorPass bool
	EntryMaxKB                         int
	ListMaxBytes                       int64
	ReadPatch                          func(int64) ([]byte, error)
}

// UpdateEntry applies a visitor patch and records its previous value.
func (a *Adapter) UpdateEntry(ctx context.Context, in UpdateEntryInput) (db.CollectionItem, db.DataSettings, error) {
	set, err := a.visitorEntrySettings(ctx, in.SiteID, in.Name, in.DeclareFirst, in.VisitorAccess)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CollectionItem{}, set, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return db.CollectionItem{}, set, err
	}
	if !in.ValidID {
		return db.CollectionItem{}, set, &Error{Code: "invalid_id"}
	}
	if err := a.requireWritableEntry(ctx, in.SiteID, in.Name, in.ID, in.VisitorAccess.UserID, in.VisitorPass, true); err != nil {
		return db.CollectionItem{}, set, err
	}
	maxBytes := EntryMaxBytes(set, in.EntryMaxKB, in.ListMaxBytes)
	raw, err := in.ReadPatch(maxBytes)
	if err != nil {
		return db.CollectionItem{}, set, err
	}
	patch, ok := storagerules.DecodeLegacyEntryPatch(raw)
	if !ok {
		return db.CollectionItem{}, set, &Error{Code: "invalid_patch"}
	}
	if !storagerules.LegacyJSONStorable(raw) {
		return db.CollectionItem{}, set, &Error{Code: "invalid_json"}
	}
	if a.deps.BoundHistory != nil {
		defer a.deps.BoundHistory(ctx, in.SiteID)
	}
	actor := a.entryActor(ctx, in.VisitorAccess.UserID)
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return db.CollectionItem{}, set, err
	}
	defer tx.Rollback()
	cur, err := repo.LockCollectionItem(ctx, tx, in.SiteID, in.Name, in.ID)
	if err != nil {
		return db.CollectionItem{}, set, err
	}
	next, err := storagerules.PatchLegacyEntry(cur.Data, patch, maxBytes)
	if err != nil {
		return db.CollectionItem{}, set, err
	}
	it, err := repo.UpdateCollectionItem(ctx, tx, in.SiteID, in.Name, in.ID, next)
	if err != nil {
		return db.CollectionItem{}, set, err
	}
	if err := repo.RecordHistory(ctx, tx, in.SiteID, db.HistoryList, in.Name, &in.ID, db.OpEdit, cur.Data, actor); err != nil {
		return db.CollectionItem{}, set, err
	}
	if len(it.Data) > len(cur.Data) && a.deps.MaxBytes > 0 {
		used, err := repo.SiteDataUsage(ctx, tx, in.SiteID)
		if err != nil {
			return db.CollectionItem{}, set, err
		}
		if used > a.deps.MaxBytes {
			return db.CollectionItem{}, set, db.ErrSiteFull
		}
	}
	return it, set, tx.Commit()
}

// WithdrawEntryInput identifies the signed-in visitor and entry to withdraw.
type WithdrawEntryInput struct {
	SiteID, Name          string
	VisitorAccess         VisitorAccessInput
	ID                    int64
	ValidID, DeclareFirst bool
}

// WithdrawEntry checks the declaration and author before withdrawing an entry.
func (a *Adapter) WithdrawEntry(ctx context.Context, in WithdrawEntryInput) (db.DataSettings, error) {
	set, err := a.visitorEntrySettings(ctx, in.SiteID, in.Name, in.DeclareFirst, in.VisitorAccess)
	if err != nil {
		return set, err
	}
	if !in.ValidID {
		return set, &Error{Code: "invalid_id"}
	}
	if err := a.requireOwnedEntry(ctx, in.SiteID, in.Name, in.ID, in.VisitorAccess.UserID, true); err != nil {
		return set, err
	}
	actor := a.entryActor(ctx, in.VisitorAccess.UserID)
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return set, &Error{Code: "delete_failed"}
	}
	defer tx.Rollback()
	res, err := repo.SoftDeleteItem(ctx, tx, in.SiteID, in.Name, in.ID)
	if err != nil {
		return set, &Error{Code: "delete_failed"}
	}
	n, err := res.RowsAffected()
	if err != nil {
		return set, &Error{Code: "delete_failed"}
	}
	if n == 0 {
		return set, &Error{Code: "not_found"}
	}
	if err := repo.RecordHistory(ctx, tx, in.SiteID, db.HistoryList, in.Name, &in.ID, db.OpDelete, nil, actor); err != nil {
		return set, &Error{Code: "delete_failed"}
	}
	if err := tx.Commit(); err != nil {
		return set, &Error{Code: "delete_failed"}
	}
	return set, nil
}

// visitorEntrySettings preserves the access check before the declaration read.
func (a *Adapter) visitorEntrySettings(ctx context.Context, siteID, name string, declareFirst bool, access VisitorAccessInput) (db.DataSettings, error) {
	if err := a.CheckVisitorAccess(ctx, access); err != nil {
		return db.DataSettings{}, err
	}
	set, err := a.WriteCollectionSettings(ctx, siteID, name, declareFirst)
	if err != nil {
		return set, err
	}
	if !storagerules.LegacyEntriesLike(set.Kind, set.Shared) {
		return set, &Error{Code: "wrong_kind"}
	}
	return set, nil
}

// requireOwnedEntry checks the author before an entry mutation.
func (a *Adapter) requireOwnedEntry(ctx context.Context, siteID, name string, id int64, userID string, live bool) error {
	author, err := repo.GetItemAuthor(ctx, a.database, siteID, name, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || !storagerules.LegacyEntryOwned(author.SubmittedBy, userID, author.DeletedAt != nil, live) {
		return &Error{Code: "not_found"}
	}
	return nil
}

// requireWritableEntry checks ownership before the site's saver policy.
func (a *Adapter) requireWritableEntry(ctx context.Context, siteID, name string, id int64, userID string, visitorPass, live bool) error {
	if err := a.requireOwnedEntry(ctx, siteID, name, id, userID, live); err != nil {
		return err
	}
	allowed, err := a.PersonSaverAllowed(ctx, PersonSaverInput{SiteID: siteID, UserID: userID, VisitorPass: visitorPass})
	if err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if !allowed {
		return &Error{Code: "not_allowed"}
	}
	return nil
}

// entryActor resolves the optional author email used in entry history.
func (a *Adapter) entryActor(ctx context.Context, userID string) db.Actor {
	actor := db.Actor{ID: userID, Kind: "visitor"}
	if userID != "" {
		actor.Email, _ = a.visitorEmail(ctx, userID)
	}
	return actor
}

// visitorEmail resolves the verified email used by entry permissions and history.
func (a *Adapter) visitorEmail(ctx context.Context, userID string) (string, error) {
	u, err := db.GetUserByID(ctx, a.database, userID)
	if err != nil {
		return "", err
	}
	email := u.Username
	if !strings.Contains(email, "@") {
		identity, err := db.GetLatestOAuthIdentity(ctx, a.database, userID)
		if err == nil && identity.Email.Valid && identity.Email.String != "" {
			email = identity.Email.String
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return email, nil
}

// UndoEntryInput identifies a withdrawn entry and its restoration limits.
type UndoEntryInput struct {
	SiteID, Name                       string
	VisitorAccess                      VisitorAccessInput
	ID                                 int64
	Window                             time.Duration
	ValidID, DeclareFirst, VisitorPass bool
	MaxItems                           int
	MaxBytes                           int64
}

// UndoEntry restores a visitor's withdrawal within its permitted window.
func (a *Adapter) UndoEntry(ctx context.Context, in UndoEntryInput) (db.CollectionItem, bool, db.DataSettings, error) {
	set, err := a.visitorEntrySettings(ctx, in.SiteID, in.Name, in.DeclareFirst, in.VisitorAccess)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CollectionItem{}, false, set, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return db.CollectionItem{}, false, set, err
	}
	if !in.ValidID {
		return db.CollectionItem{}, false, set, &Error{Code: "invalid_id"}
	}
	if err := a.requireWritableEntry(ctx, in.SiteID, in.Name, in.ID, in.VisitorAccess.UserID, in.VisitorPass, false); err != nil {
		return db.CollectionItem{}, false, set, err
	}
	entryActor := a.entryActor(ctx, in.VisitorAccess.UserID)
	declared := set.Kind == db.KindEntries
	onePerPerson := declared && set.OnePerPerson
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return db.CollectionItem{}, false, set, err
	}
	defer tx.Rollback()
	if declared {
		if err := repo.LockContentDeclaration(ctx, tx, in.SiteID, in.Name); err != nil {
			return db.CollectionItem{}, false, set, err
		}
	}
	op, actor, at, err := repo.LockWithdrawal(ctx, tx, in.SiteID, in.Name, in.ID, in.VisitorAccess.UserID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CollectionItem{}, false, set, nil
	}
	if err != nil {
		return db.CollectionItem{}, false, set, err
	}
	if !storagerules.LegacyWithdrawalRestorable(op.String, actor.String, in.VisitorAccess.UserID, at, in.Window) {
		return db.CollectionItem{}, false, set, nil
	}
	if declared && onePerPerson {
		other, _, err := repo.LivePersonEntry(ctx, tx, in.SiteID, in.Name, in.VisitorAccess.UserID, db.BaseEmail(entryActor.Email), in.ID)
		if err != nil {
			return db.CollectionItem{}, false, set, err
		}
		if other != 0 {
			return db.CollectionItem{}, false, set, db.ErrOnePerPerson
		}
	}
	if declared {
		full, err := repo.NameHoldsItems(ctx, tx, in.SiteID, in.Name, in.MaxItems-1)
		if err != nil {
			return db.CollectionItem{}, false, set, err
		}
		if full {
			return db.CollectionItem{}, false, set, db.ErrNameFull
		}
	}
	it, err := repo.RestoreWithdrawal(ctx, tx, in.ID)
	if err != nil {
		return db.CollectionItem{}, false, set, err
	}
	if err := repo.RecordHistory(ctx, tx, in.SiteID, db.HistoryList, in.Name, &in.ID, db.OpUndelete, nil, entryActor); err != nil {
		return db.CollectionItem{}, false, set, err
	}
	if in.MaxBytes > 0 {
		used, err := repo.SiteDataUsage(ctx, tx, in.SiteID)
		if err != nil {
			return db.CollectionItem{}, false, set, err
		}
		if used > in.MaxBytes {
			return db.CollectionItem{}, false, set, db.ErrSiteFull
		}
	}
	return it, true, set, tx.Commit()
}
