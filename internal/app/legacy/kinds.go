package legacy

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	model "github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	siterepo "github.com/vsriram/simple-host/internal/repository/sites"
	storagerules "github.com/vsriram/simple-host/internal/rules/storage"
)

// VisitorAccessInput contains the session and request facts for a visitor write.
type VisitorAccessInput struct {
	UserID              string
	SessionID           []byte
	NeedCSRF, CSRFValid bool
	Idle                time.Duration
}

// CheckVisitorAccess checks suspension and CSRF before touching the session.
func (a *Adapter) CheckVisitorAccess(ctx context.Context, in VisitorAccessInput) error {
	suspended, err := a.accounts.UserSuspended(ctx, in.UserID)
	if err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if suspended {
		return &Error{Code: "account_suspended"}
	}
	if in.NeedCSRF && !in.CSRFValid {
		return &Error{Code: "csrf_required"}
	}
	_ = a.accounts.TouchVisitorSession(ctx, in.SessionID, in.Idle)
	return nil
}

// SaverInput identifies a site and the email to check.
type SaverInput struct {
	SiteID, Email string
}

// SaverAllowed checks whether an email may save on a site.
func (a *Adapter) SaverAllowed(ctx context.Context, in SaverInput) (bool, error) {
	return a.saverAllowed(ctx, in.SiteID, in.Email)
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
	ok, err := a.saverAllowed(ctx, in.SiteID, email)
	if err != nil || ok {
		return ok, err
	}
	if in.UserID != "" && !in.VisitorPass {
		if ownerID, _, err := siterepo.GetSiteWriteGate(ctx, a.database, in.SiteID); err == nil && ownerID == in.UserID {
			return true, nil
		}
	}
	return false, nil
}

// updateVisitorEntryInput contains request facts and a deferred body reader for an entry patch.
type updateVisitorEntryInput struct {
	SiteID, Name                       string
	VisitorAccess                      VisitorAccessInput
	ID                                 int64
	ValidID, DeclareFirst, VisitorPass bool
	EntryMaxKB                         int
	ListMaxBytes                       int64
	ReadPatch                          func(int64) ([]byte, error)
}

// updateVisitorEntry applies a visitor patch and records its previous value.
func (a *Adapter) updateVisitorEntry(ctx context.Context, in updateVisitorEntryInput) (model.CollectionItem, model.DataSettings, error) {
	set, err := a.visitorEntrySettings(ctx, in.SiteID, in.Name, in.DeclareFirst, in.VisitorAccess)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, set, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return model.CollectionItem{}, set, err
	}
	if !in.ValidID {
		return model.CollectionItem{}, set, &Error{Code: "invalid_id"}
	}
	if err := a.requireWritableEntry(ctx, in.SiteID, in.Name, in.ID, in.VisitorAccess.UserID, in.VisitorPass, true); err != nil {
		return model.CollectionItem{}, set, err
	}
	maxBytes := EntryMaxBytes(set, in.EntryMaxKB, in.ListMaxBytes)
	raw, readErr := in.ReadPatch(maxBytes)
	var bodyErr *Error
	if errors.As(readErr, &bodyErr) && bodyErr.Code == "item_too_large" {
		return model.CollectionItem{}, set, &Error{Code: "item_too_large"}
	}
	if readErr != nil {
		return model.CollectionItem{}, set, &Error{Code: "invalid_patch"}
	}
	patch, valid := storagerules.DecodeLegacyEntryPatch(raw)
	if !valid {
		return model.CollectionItem{}, set, &Error{Code: "invalid_patch"}
	}
	if !storagerules.LegacyJSONStorable(raw) {
		return model.CollectionItem{}, set, &Error{Code: "invalid_json"}
	}
	if a.deps.BoundHistory != nil {
		defer a.deps.BoundHistory(ctx, in.SiteID)
	}
	actor := a.entryActor(ctx, in.VisitorAccess.UserID)
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, set, err
	}
	defer tx.Rollback()
	cur, err := repo.LockCollectionItem(ctx, tx, in.SiteID, in.Name, in.ID)
	if err != nil {
		return model.CollectionItem{}, set, err
	}
	next, err := storagerules.PatchLegacyEntry(cur.Data, patch, maxBytes)
	if err != nil {
		return model.CollectionItem{}, set, err
	}
	it, err := repo.UpdateCollectionItem(ctx, tx, in.SiteID, in.Name, in.ID, next)
	if err != nil {
		return model.CollectionItem{}, set, err
	}
	if err := repo.RecordHistory(ctx, tx, in.SiteID, "list", in.Name, &in.ID, "edit", cur.Data, actor); err != nil {
		return model.CollectionItem{}, set, err
	}
	if len(it.Data) > len(cur.Data) && a.deps.MaxBytes() > 0 {
		used, err := repo.SiteDataUsage(ctx, tx, in.SiteID)
		if err != nil {
			return model.CollectionItem{}, set, err
		}
		if used > a.deps.MaxBytes() {
			return model.CollectionItem{}, set, model.ErrSiteFull
		}
	}
	return it, set, tx.Commit()
}

// withdrawVisitorEntryInput identifies the signed-in visitor and entry to withdraw.
type withdrawVisitorEntryInput struct {
	SiteID, Name          string
	VisitorAccess         VisitorAccessInput
	ID                    int64
	ValidID, DeclareFirst bool
}

// withdrawVisitorEntry checks the declaration and author before withdrawing an entry.
func (a *Adapter) withdrawVisitorEntry(ctx context.Context, in withdrawVisitorEntryInput) (model.DataSettings, error) {
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
	if err := repo.RecordHistory(ctx, tx, in.SiteID, "list", in.Name, &in.ID, "delete", nil, actor); err != nil {
		return set, &Error{Code: "delete_failed"}
	}
	if err := tx.Commit(); err != nil {
		return set, &Error{Code: "delete_failed"}
	}
	return set, nil
}

// visitorEntrySettings preserves the access check before the declaration read.
func (a *Adapter) visitorEntrySettings(ctx context.Context, siteID, name string, declareFirst bool, access VisitorAccessInput) (model.DataSettings, error) {
	if err := a.CheckVisitorAccess(ctx, access); err != nil {
		return model.DataSettings{}, err
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
func (a *Adapter) entryActor(ctx context.Context, userID string) model.Actor {
	actor := model.Actor{ID: userID, Kind: "visitor"}
	if userID != "" {
		actor.Email, _ = a.visitorEmail(ctx, userID)
	}
	return actor
}

// visitorEmail resolves the verified email used by entry permissions and history.
func (a *Adapter) visitorEmail(ctx context.Context, userID string) (string, error) {
	u, err := a.accounts.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	email := u.Username
	if !strings.Contains(email, "@") {
		identity, err := a.accounts.GetLatestOAuthIdentity(ctx, userID)
		if err == nil && identity.Email.Valid && identity.Email.String != "" {
			email = identity.Email.String
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return email, nil
}

// undoVisitorEntryInput identifies a withdrawn entry and its restoration limits.
type undoVisitorEntryInput struct {
	SiteID, Name                       string
	VisitorAccess                      VisitorAccessInput
	ID                                 int64
	Window                             time.Duration
	ValidID, DeclareFirst, VisitorPass bool
	MaxItems                           int
	MaxBytes                           int64
}

// undoVisitorEntry restores a visitor's withdrawal within its permitted window.
func (a *Adapter) undoVisitorEntry(ctx context.Context, in undoVisitorEntryInput) (model.CollectionItem, bool, model.DataSettings, error) {
	set, err := a.visitorEntrySettings(ctx, in.SiteID, in.Name, in.DeclareFirst, in.VisitorAccess)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, false, set, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return model.CollectionItem{}, false, set, err
	}
	if !in.ValidID {
		return model.CollectionItem{}, false, set, &Error{Code: "invalid_id"}
	}
	if err := a.requireWritableEntry(ctx, in.SiteID, in.Name, in.ID, in.VisitorAccess.UserID, in.VisitorPass, false); err != nil {
		return model.CollectionItem{}, false, set, err
	}
	entryActor := a.entryActor(ctx, in.VisitorAccess.UserID)
	declared := set.Kind == "entries"
	onePerPerson := declared && set.OnePerPerson
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, false, set, err
	}
	defer tx.Rollback()
	if declared {
		if err := repo.LockContentDeclaration(ctx, tx, in.SiteID, in.Name); err != nil {
			return model.CollectionItem{}, false, set, err
		}
	}
	op, actor, at, err := repo.LockWithdrawal(ctx, tx, in.SiteID, in.Name, in.ID, in.VisitorAccess.UserID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, false, set, nil
	}
	if err != nil {
		return model.CollectionItem{}, false, set, err
	}
	if !storagerules.LegacyWithdrawalRestorable(op.String, actor.String, in.VisitorAccess.UserID, at, time.Now(), in.Window) {
		return model.CollectionItem{}, false, set, nil
	}
	if declared && onePerPerson {
		other, _, err := repo.LivePersonEntry(ctx, tx, in.SiteID, in.Name, in.VisitorAccess.UserID, model.BaseEmail(entryActor.Email), in.ID)
		if err != nil {
			return model.CollectionItem{}, false, set, err
		}
		if other != 0 {
			return model.CollectionItem{}, false, set, model.ErrOnePerPerson
		}
	}
	if declared {
		full, err := repo.NameHoldsItems(ctx, tx, in.SiteID, in.Name, in.MaxItems-1)
		if err != nil {
			return model.CollectionItem{}, false, set, err
		}
		if full {
			return model.CollectionItem{}, false, set, model.ErrNameFull
		}
	}
	it, err := repo.RestoreWithdrawal(ctx, tx, in.ID)
	if err != nil {
		return model.CollectionItem{}, false, set, err
	}
	if err := repo.RecordHistory(ctx, tx, in.SiteID, "list", in.Name, &in.ID, "undelete", nil, entryActor); err != nil {
		return model.CollectionItem{}, false, set, err
	}
	if in.MaxBytes > 0 {
		used, err := repo.SiteDataUsage(ctx, tx, in.SiteID)
		if err != nil {
			return model.CollectionItem{}, false, set, err
		}
		if used > in.MaxBytes {
			return model.CollectionItem{}, false, set, model.ErrSiteFull
		}
	}
	return it, true, set, tx.Commit()
}

// SetSaversInput identifies the site and its new saver settings.
type SetSaversInput struct {
	SiteID string
	Savers model.Savers
}

// SetSavers replaces a site's saver settings in one transaction.
func (a *Adapter) SetSavers(ctx context.Context, in SetSaversInput) error {
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := repo.SetSavers(ctx, tx, in.SiteID, in.Savers); err != nil {
		return err
	}
	return tx.Commit()
}

// saverAllowed determines whether an email may save.
func (a *Adapter) saverAllowed(ctx context.Context, siteID, email string) (bool, error) {
	mode, any, err := repo.SaverMode(ctx, a.database, siteID)
	if err != nil {
		return false, err
	}
	if !any {
		return mode != "listed", nil
	}
	email, keys, blockKeys := storagerules.LegacySaverKeys(email)
	if email == "" {
		return mode != "listed", nil
	}
	blocked, allowed, err := repo.SaverMatches(ctx, a.database, siteID, keys, blockKeys)
	if err != nil {
		return false, err
	}
	if blocked {
		return false, nil
	}
	return mode != "listed" || allowed, nil
}

// PutContentInput contains a page-info document and its author.
type PutContentInput struct {
	SiteID, Name string
	Key          string
	CSRFValid    bool
}

// PutDataInput contains request facts for either kind of document write.
type PutDataInput struct {
	PutContentInput
	Preview, ContentHost, VisitorPass bool
	Access                            VisitorAccessInput
	IdempotencyKey                    string
	PersonalPeopleMax                 int
}

// PutDataResult contains the saved document or replayed Personal record.
type PutDataResult struct {
	Item                                     model.CollectionItem
	Personal, Found, Replayed, SaveAttempted bool
	ReplayStatus                             int
}

// PutData writes a page-info document or a visitor's Personal record.
func (a *Adapter) PutData(ctx context.Context, in PutDataInput) (PutDataResult, error) {
	item, err := a.putContent(ctx, in.PutContentInput)
	var opErr *Error
	if !errors.As(err, &opErr) || opErr.Code != "content_personal" {
		return PutDataResult{Item: item}, err
	}
	return a.putPersonalData(ctx, in)
}

func (a *Adapter) putPersonalData(ctx context.Context, in PutDataInput) (result PutDataResult, err error) {
	result.Personal = true
	if in.Key != "" {
		return result, &Error{Code: "personal_only"}
	}
	offline, err := siterepo.SiteOffline(ctx, a.database, in.SiteID)
	if err != nil {
		return result, &Error{Code: "internal", Message: err.Error()}
	}
	if offline {
		return result, &Error{Code: "site_offline"}
	}
	if in.Preview {
		return result, &Error{Code: "preview_read_only"}
	}
	if in.ContentHost {
		return result, &Error{Code: "visitor_auth_required"}
	}
	sess, ok := a.deps.PersonalSession(in.SiteID)
	if !ok {
		return result, &Error{Code: "visitor_auth_required"}
	}
	in.Access.UserID, in.Access.SessionID = sess.UserID, sess.ID
	if err := a.CheckVisitorAccess(ctx, in.Access); err != nil {
		return result, err
	}
	ok, err = a.PersonSaverAllowed(ctx, PersonSaverInput{SiteID: in.SiteID, UserID: sess.UserID, VisitorPass: in.VisitorPass})
	if err != nil {
		return result, &Error{Code: "internal", Message: err.Error()}
	}
	if !ok {
		return result, &Error{Code: "not_allowed_to_save"}
	}
	if !a.deps.PersonalWriteLimit() {
		return result, &Error{Code: "rate_limited"}
	}
	actor := a.deps.ContentAuthor(ctx, model.Actor{ID: sess.UserID, Kind: "visitor"})
	raw, doc, err := a.deps.PersonalReadBody()
	if err != nil {
		return result, err
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 255 {
		return result, &Error{Code: "invalid_idempotency_key"}
	}
	var scope []byte
	if key != "" && actor.ID != "" {
		var previous model.LegacyIdempotentResponse
		var state string
		scope, previous, state, err = a.ClaimWrite(ctx, in.SiteID, "PUT data/"+in.Name, actor.ID, key, raw)
		if err != nil {
			return result, &Error{Code: "internal", Message: err.Error()}
		}
		switch state {
		case "reused":
			return result, &Error{Code: "idempotency_key_reused"}
		case "pending":
			return result, &Error{Code: "idempotency_in_progress"}
		case "replay":
			result.Replayed, result.ReplayStatus = true, previous.Status
			result.Item, result.Found, err = repo.GetPersonal(ctx, a.database, in.SiteID, in.Name, actor.ID)
			if err != nil {
				return result, &Error{Code: "internal", Message: err.Error()}
			}
			return result, err
		}
	}
	saved := false
	if scope != nil {
		defer func() {
			if !saved {
				if e := a.ReleaseIdempotencyKey(context.WithoutCancel(ctx), scope); e != nil {
					log.Printf("idempotency release: %v", e)
				}
			}
		}()
	}
	result.SaveAttempted = true
	result.Item, err = a.savePersonalDocument(ctx, in, actor, doc, a.deps.ContentSiteMaxBytes())
	a.deps.BoundHistory(ctx, in.SiteID)
	if err != nil {
		return result, err
	}
	result.Found = true
	if scope != nil {
		// Keep it even if the caller has gone: that is the retry case.
		if e := a.SaveIdempotentResponse(context.WithoutCancel(ctx), scope, 200, "", result.Item.ID); e != nil {
			log.Printf("idempotency save: %v", e)
		}
	}
	saved = true
	return result, nil
}

func (a *Adapter) savePersonalDocument(ctx context.Context, in PutDataInput, actor model.Actor, doc json.RawMessage, maxBytes int64) (model.CollectionItem, error) {
	if actor.ID == "" {
		return model.CollectionItem{}, errors.New("a personal record needs a signed-in person")
	}
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, err
	}
	defer tx.Rollback()
	if err := repo.LockPersonalDeclaration(ctx, tx, in.SiteID, in.Name); err != nil {
		return model.CollectionItem{}, err
	}
	id, old, deleted, err := repo.LockPersonalItem(ctx, tx, in.SiteID, in.Name, actor.ID)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, err
	}
	var it model.CollectionItem
	if found {
		it, err = repo.UpdatePersonalItem(ctx, tx, id, doc, actor.Email)
		if err != nil {
			return model.CollectionItem{}, err
		}
		if err := repo.RecordHistory(ctx, tx, in.SiteID, "list", in.Name, &id, "edit", old, actor); err != nil {
			return model.CollectionItem{}, err
		}
	} else {
		if in.PersonalPeopleMax > 0 {
			n, err := repo.CountPersonalItems(ctx, tx, in.SiteID, in.Name, in.PersonalPeopleMax)
			if err != nil {
				return model.CollectionItem{}, err
			}
			if n >= in.PersonalPeopleMax {
				return model.CollectionItem{}, model.ErrPeopleFull
			}
		}
		it, err = repo.InsertPersonalItem(ctx, tx, in.SiteID, in.Name, doc, actor)
		if err != nil {
			return model.CollectionItem{}, err
		}
	}
	if !found || deleted.Valid || len(it.Data) > len(old) {
		if maxBytes > 0 {
			used, err := repo.SiteDataUsage(ctx, tx, in.SiteID)
			if err != nil {
				return model.CollectionItem{}, err
			}
			if used > maxBytes {
				return model.CollectionItem{}, model.ErrSiteFull
			}
		}
	}
	return it, tx.Commit()
}

// putContent stores a page-info document and bounds its history afterward.
func (a *Adapter) putContent(ctx context.Context, in PutContentInput) (model.CollectionItem, error) {
	suspended, reason, err := siterepo.SiteSuspension(ctx, a.database, in.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return model.CollectionItem{}, err
	}
	if suspended {
		return model.CollectionItem{}, &Error{Code: "site_suspended", Message: reason}
	}

	set, err := a.WriteCollectionSettings(ctx, in.SiteID, in.Name, a.deps.ContentDeclareFirst())
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectionItem{}, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return model.CollectionItem{}, err
	}
	switch set.Kind {
	case "content":
	case "mine":
		return model.CollectionItem{}, &Error{Code: "content_personal"}
	case "entries", "board":
		return model.CollectionItem{}, &Error{Code: "content_wrong_kind", Message: set.Kind}
	default:
		return model.CollectionItem{}, &Error{Code: "content_declare_first"}
	}

	ownerID, _, err := siterepo.GetSiteWriteGate(ctx, a.database, in.SiteID)
	if err != nil {
		return model.CollectionItem{}, &Error{Code: "site_not_found"}
	}
	var actor model.Actor
	if in.Key != "" {
		u, ok, err := a.deps.VisitorResolveKey(ctx, in.Key)
		if err != nil {
			return model.CollectionItem{}, err
		}
		if !ok {
			return model.CollectionItem{}, &Error{Code: "invalid_api_key"}
		}
		if !u.IsAdmin && u.ID != ownerID {
			return model.CollectionItem{}, &Error{Code: "site_not_found"}
		}
		actor = model.Actor{ID: u.ID, Kind: "owner"}
		if u.ID != ownerID {
			actor.Kind = "admin"
		}
	} else if a.deps.ContentOwnerBrowser(in.SiteID) {
		if !in.CSRFValid {
			return model.CollectionItem{}, &Error{Code: "csrf_required"}
		}
		actor = model.Actor{ID: ownerID, Kind: "owner"}
	} else {
		return model.CollectionItem{}, &Error{Code: "owner_only"}
	}
	actor = a.deps.ContentAuthor(ctx, actor)
	data, err := a.deps.ContentBody()
	if err != nil {
		return model.CollectionItem{}, err
	}
	maxBytes := a.deps.ContentSiteMaxBytes()
	if a.deps.BoundHistory != nil {
		defer a.deps.BoundHistory(ctx, in.SiteID)
	}
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return model.CollectionItem{}, err
	}
	defer tx.Rollback()
	if err := repo.LockContentForPut(ctx, tx, in.SiteID, in.Name); err != nil {
		return model.CollectionItem{}, err
	}
	cur, found, err := repo.GetContent(ctx, tx, in.SiteID, in.Name)
	if err != nil {
		return model.CollectionItem{}, err
	}
	var it model.CollectionItem
	if found {
		it, err = repo.UpdateContent(ctx, tx, cur.ID, data, actor)
		if err != nil {
			return model.CollectionItem{}, err
		}
		if err := repo.RecordHistory(ctx, tx, in.SiteID, "list", in.Name, &cur.ID, "edit", cur.Data, actor); err != nil {
			return model.CollectionItem{}, err
		}
	} else {
		it, err = repo.InsertContent(ctx, tx, in.SiteID, in.Name, data, actor)
		if err != nil {
			return model.CollectionItem{}, err
		}
	}
	if (!found || len(it.Data) > len(cur.Data)) && maxBytes > 0 {
		used, err := repo.SiteDataUsage(ctx, tx, in.SiteID)
		if err != nil {
			return model.CollectionItem{}, err
		}
		if used > maxBytes {
			return model.CollectionItem{}, model.ErrSiteFull
		}
	}
	return it, tx.Commit()
}

// ListMineInput identifies a visitor and a page of their entries.
type ListMineInput struct {
	SiteName, Name string
	Access         VisitorAccessInput
	ContentHost    bool
	DeclareFirst   bool
	Limit          int
	Before         int64
}

// ListMineResult holds a page of a visitor's entries and its continuation.
type ListMineResult struct {
	Items    []model.CollectionItem
	Next     *int64
	Settings model.DataSettings
}

// ListMine checks visitor access and reads the visitor's entries.
func (a *Adapter) ListMine(ctx context.Context, in ListMineInput) (ListMineResult, error) {
	siteID, err := a.deps.MineResolveSite(in.SiteName)
	if err != nil {
		return ListMineResult{}, &Error{Code: "site_not_found"}
	}
	if !a.deps.MineReadLimit(siteID) {
		return ListMineResult{}, &Error{Code: "rate_limited"}
	}
	suspended, reason, err := siterepo.SiteSuspension(ctx, a.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return ListMineResult{}, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return ListMineResult{}, err
	}
	if suspended {
		return ListMineResult{}, &Error{Code: "site_suspended", Message: reason}
	}
	if in.ContentHost {
		return ListMineResult{}, &Error{Code: "visitor_auth_required"}
	}
	sess, ok := a.deps.ResolveMineSession(siteID)
	if !ok {
		return ListMineResult{}, &Error{Code: "visitor_auth_required"}
	}
	in.Access.UserID, in.Access.SessionID = sess.UserID, sess.ID
	if err := a.CheckVisitorAccess(ctx, in.Access); err != nil {
		return ListMineResult{}, err
	}
	set, err := a.WriteCollectionSettings(ctx, siteID, in.Name, in.DeclareFirst)
	if errors.Is(err, sql.ErrNoRows) {
		return ListMineResult{Settings: set}, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return ListMineResult{Settings: set}, err
	}
	if !storagerules.LegacyEntriesLike(set.Kind, set.Shared) && set.Kind != "board" {
		return ListMineResult{Settings: set}, &Error{Code: "wrong_kind"}
	}
	items, err := repo.ListOwnEntries(ctx, a.database, siteID, in.Name, in.Access.UserID, in.Limit, in.Before)
	result := ListMineResult{Items: items, Settings: set}
	if len(items) == in.Limit && in.Limit > 0 {
		n := items[len(items)-1].ID
		result.Next = &n
	}
	return result, err
}

// DataKindInput identifies the declaration to describe.
type DataKindInput struct {
	SiteID, Name string
	OwnerKey     bool
	DeclareFirst bool
}

// GetDataKindInput contains request facts for a declaration read.
type GetDataKindInput struct {
	SiteName, Name                      string
	ValidName, Originless, DeclareFirst bool
}

// GetDataKind resolves and authorizes the page-facing declaration read.
func (a *Adapter) GetDataKind(ctx context.Context, in GetDataKindInput) (DataKindResult, error) {
	siteID, ownerKey, err := a.resolveDataReadSite(in)
	if err != nil {
		return DataKindResult{}, err
	}
	if !a.deps.KindReadLimit(siteID) {
		return DataKindResult{}, &Error{Code: "rate_limited"}
	}
	return a.DataKind(ctx, DataKindInput{SiteID: siteID, Name: in.Name, OwnerKey: ownerKey, DeclareFirst: in.DeclareFirst})
}

func (a *Adapter) resolveDataReadSite(in GetDataKindInput) (string, bool, error) {
	if in.SiteName == "" || !in.ValidName {
		return "", false, &Error{Code: "invalid_site_or_name"}
	}
	siteID, ownerKey := a.deps.KindOwnerSite(in.SiteName)
	if !ownerKey {
		if !in.Originless && !a.deps.KindOriginAllowed(in.SiteName) {
			return "", false, &Error{Code: "origin_not_allowed"}
		}
		var err error
		siteID, err = a.deps.KindResolveSite(in.SiteName)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, &Error{Code: "site_not_found"}
		}
		if err != nil {
			return "", false, err
		}
	}
	return siteID, ownerKey, nil
}

// DataKindResult contains the declaration facts shown to readers.
type DataKindResult struct {
	Settings     model.DataSettings
	EntriesLike  bool
	OnePerPerson bool
}

// DataReadInput identifies a page-facing data read.
type DataReadInput struct {
	GetDataKindInput
	Count, ContentHost bool
	Access             VisitorAccessInput
	Limit, Before      string
}

// DataReadResult contains the selected route and any data it read.
type DataReadResult struct {
	SiteID                         string
	OwnerKey                       bool
	Route                          storagerules.LegacyDataRoute
	Document                       model.CollectionItem
	Found, OwnerView, PrivateCache bool
	PersonalCount, PersonalFew     bool
	Items                          []model.CollectionItem
	Next                           *int64
	Private, Board, WithAuthor     bool
	Count                          int64
}

// ReadData checks the declaration and reads page-info or count data.
func (a *Adapter) ReadData(ctx context.Context, in DataReadInput) (DataReadResult, error) {
	siteID, ownerKey, err := a.resolveDataReadSite(in.GetDataKindInput)
	if err != nil {
		return DataReadResult{}, err
	}
	kind, err := a.DataKind(ctx, DataKindInput{SiteID: siteID, Name: in.Name, OwnerKey: ownerKey, DeclareFirst: in.DeclareFirst})
	if err != nil {
		return DataReadResult{}, err
	}
	set := kind.Settings
	if storagerules.LegacyDataReadLimited(set.Kind, set.Shared, in.Count) && !a.deps.DataReadLimit(siteID) {
		return DataReadResult{}, &Error{Code: "rate_limited"}
	}
	result := DataReadResult{SiteID: siteID, OwnerKey: ownerKey, Route: storagerules.LegacyDataReadRoute(set.Kind, set.Shared, in.Count)}
	switch result.Route {
	case storagerules.LegacyDataList:
		return a.readDataList(ctx, in)
	case storagerules.LegacyDataPersonal:
		result.PrivateCache = true
		if ownerKey || a.deps.DataOwnerBrowser(siteID) {
			if in.Count {
				result.PersonalCount = true
				result.Count, err = repo.CountLiveItems(ctx, a.database, siteID, in.Name)
				result.PersonalFew = result.Count > 0 && result.Count < repo.PersonalFewMax
				return result, err
			}
			if ownerKey {
				return result, &Error{Code: "personal_only"}
			}
		}
		if in.ContentHost {
			return result, &Error{Code: "visitor_auth_required"}
		}
		sess, ok := a.deps.ResolveMineSession(siteID)
		if !ok {
			return result, &Error{Code: "visitor_auth_required"}
		}
		in.Access.UserID, in.Access.SessionID = sess.UserID, sess.ID
		if err := a.CheckVisitorAccess(ctx, in.Access); err != nil {
			return result, err
		}
		result.Document, result.Found, err = repo.GetPersonal(ctx, a.database, siteID, in.Name, sess.UserID)
	case storagerules.LegacyDataContent:
		result.Document, result.Found, err = repo.GetContent(ctx, a.database, siteID, in.Name)
		if err == nil && result.Found {
			result.OwnerView = ownerKey || a.deps.DataOwnerBrowser(siteID)
			result.PrivateCache = result.OwnerView
		}
	case storagerules.LegacyDataCount:
		if set.Private && !ownerKey && !a.deps.DataOwnerBrowser(siteID) {
			result.PrivateCache = true
			return result, &Error{Code: "private_not_found"}
		}
		result.Count, err = repo.CountLiveItems(ctx, a.database, siteID, in.Name)
	}
	return result, err
}

func (a *Adapter) readDataList(ctx context.Context, in DataReadInput) (DataReadResult, error) {
	result := DataReadResult{Route: storagerules.LegacyDataList}
	siteID, ownerKey, err := a.resolveDataReadSite(in.GetDataKindInput)
	if err != nil {
		return result, err
	}
	if a.deps.DataAdminMismatch(siteID) {
		return result, &Error{Code: "site_not_found"}
	}
	if !a.deps.DataReadLimit(siteID) {
		return result, &Error{Code: "rate_limited"}
	}
	if !ownerKey {
		suspended, reason, err := siterepo.SiteSuspension(ctx, a.database, siteID)
		if errors.Is(err, sql.ErrNoRows) {
			return result, &Error{Code: "site_not_found"}
		}
		if err != nil {
			return result, &Error{Code: "internal", Message: err.Error()}
		}
		if suspended {
			return result, &Error{Code: "site_suspended", Message: reason}
		}
		offline, err := siterepo.SiteOffline(ctx, a.database, siteID)
		if err != nil {
			return result, &Error{Code: "internal", Message: err.Error()}
		}
		if offline {
			return result, &Error{Code: "site_offline"}
		}
	}
	set, err := a.collectionSettings(ctx, siteID, in.Name)
	if err == nil && set.Kind == "mine" {
		return result, &Error{Code: "personal_only"}
	}
	if err != nil {
		return result, &Error{Code: "internal", Message: err.Error()}
	}
	result.Private = set.Private
	if set.Private {
		result.PrivateCache = true
		if (ownerKey && in.ContentHost) || (!ownerKey && in.Originless) {
			return result, &Error{Code: "private_not_found"}
		}
		if !ownerKey {
			sess, err := a.deps.DataPrivateRead(ctx, siteID)
			if err != nil {
				var opErr *Error
				if errors.As(err, &opErr) {
					return result, err
				}
				return result, &Error{Code: "internal", Message: err.Error()}
			}
			_ = a.accounts.TouchVisitorSession(ctx, sess.ID, in.Access.Idle)
		}
	}
	result.WithAuthor = ownerKey || a.deps.DataOwnerBrowser(siteID)
	if result.WithAuthor {
		result.PrivateCache = true
	}
	result.Items, result.Next, err = a.listCollection(ctx, CollectionListInput{SiteID: siteID, Collection: in.Name, Limit: in.Limit, Before: in.Before, WithAuthor: result.WithAuthor, Settings: set})
	result.Board = set.Kind == "board"
	if err != nil {
		return result, &Error{Code: "internal", Message: err.Error()}
	}
	return result, nil
}

// DataKind reads a declaration and derives its public kind facts.
func (a *Adapter) DataKind(ctx context.Context, in DataKindInput) (DataKindResult, error) {
	if !in.OwnerKey {
		suspended, reason, err := siterepo.SiteSuspension(ctx, a.database, in.SiteID)
		if errors.Is(err, sql.ErrNoRows) {
			return DataKindResult{}, &Error{Code: "site_not_found"}
		}
		if err != nil {
			return DataKindResult{}, &Error{Code: "internal", Message: err.Error()}
		}
		if suspended {
			return DataKindResult{}, &Error{Code: "site_suspended", Message: reason}
		}
		offline, err := siterepo.SiteOffline(ctx, a.database, in.SiteID)
		if err != nil {
			return DataKindResult{}, &Error{Code: "internal", Message: err.Error()}
		}
		if offline {
			return DataKindResult{}, &Error{Code: "site_offline"}
		}
	}
	set, err := a.WriteCollectionSettings(ctx, in.SiteID, in.Name, in.DeclareFirst)
	if err != nil {
		return DataKindResult{}, err
	}
	return DataKindResult{
		Settings:     set,
		EntriesLike:  storagerules.LegacyEntriesLike(set.Kind, set.Shared),
		OnePerPerson: set.OnePerPerson && set.Kind == "entries",
	}, nil
}

// VisitorWriteInput contains request facts for a visitor write.
type VisitorWriteInput struct {
	VisitorSessionCookie                                    string
	SiteID, SiteName, Route, Collection, Mode, Key          string
	Preview, ContentHost, Canonical, VisitorPass, CSRFValid bool
	SessionIdle, KeyIdle                                    time.Duration
}

// VisitorWrite authorizes a page write and checks the site's saver policy.
func (a *Adapter) VisitorWrite(ctx context.Context, in VisitorWriteInput) (model.Actor, error) {
	anon := model.Actor{Kind: "anonymous"}
	ownerID, allowAnon, err := siterepo.GetSiteWriteGate(ctx, a.database, in.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return anon, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return anon, err
	}
	suspended, reason, err := siterepo.SiteSuspension(ctx, a.database, in.SiteID)
	if errors.Is(err, sql.ErrNoRows) {
		return anon, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return anon, err
	}
	if suspended {
		return anon, &Error{Code: "site_suspended", Message: reason}
	}
	off, err := siterepo.SiteOffline(ctx, a.database, in.SiteID)
	if err != nil {
		return anon, err
	}
	if off && !a.deps.VisitorOwnerKeyWrite(ownerID) {
		return anon, &Error{Code: "site_offline"}
	}
	if in.Preview && in.Key == "" {
		return anon, &Error{Code: "preview_read_only"}
	}
	mode := in.Mode
	if mode == "" {
		mode = "log"
	}
	domain, elsewhere := a.deps.VisitorDomainElsewhere(in.SiteID)
	if elsewhere {
		a.deps.VisitorLog(mode, "use_custom_domain")
		if mode == "on" && !allowAnon {
			return anon, &Error{Code: "use_custom_domain", Message: domain}
		}
		return a.visitorSaver(ctx, in, a.deps.VisitorKeyActor(ownerID))
	}
	if in.ContentHost {
		if info, ok, _ := siterepo.GetSiteDomainInfo(ctx, a.database, in.SiteID); ok && info.Domain != "" {
			if own, has, _ := a.deps.VisitorOwnAddress(ctx, in.SiteID); has {
				info.Domain = own.Domain
			}
			a.deps.VisitorLog(mode, "use_custom_domain")
			if mode == "on" && !allowAnon {
				return anon, &Error{Code: "use_custom_domain", Message: info.Domain}
			}
			return a.visitorSaver(ctx, in, a.deps.VisitorKeyActor(ownerID))
		}
	}
	if in.VisitorPass {
		sess, ok := a.deps.VisitorInternalSession(in.SiteID)
		if !ok {
			return anon, &Error{Code: "visitor_pass_forbidden"}
		}
		if !in.CSRFValid {
			return anon, &Error{Code: "pass_csrf_required"}
		}
		return a.visitorSaver(ctx, in, model.Actor{ID: sess.UserID, Kind: "visitor"})
	}
	if in.Key != "" {
		u, ok, err := a.deps.VisitorResolveKey(ctx, in.Key)
		if err != nil {
			return anon, err
		}
		if ok && !u.IsAdmin && u.ID != ownerID {
			a.deps.VisitorLogKey(u.ID, false)
			return anon, &Error{Code: "site_not_found"}
		}
		if ok {
			a.deps.VisitorLogKey(u.ID, true)
			if u.IsAdmin && u.ID != ownerID {
				return a.visitorSaver(ctx, in, model.Actor{ID: u.ID, Kind: "admin"})
			}
			return a.visitorSaver(ctx, in, model.Actor{ID: u.ID, Kind: "owner"})
		}
		return anon, &Error{Code: "invalid_api_key"}
	}
	if mode == "off" {
		return a.visitorSaver(ctx, in, anon)
	}
	if in.ContentHost && !in.Canonical {
		a.deps.VisitorLog(mode, "public_host")
		return a.visitorSaver(ctx, in, anon)
	}
	if raw := in.VisitorSessionCookie; raw != "" && !in.ContentHost {
		if id, decErr := hex.DecodeString(raw); decErr == nil && len(id) == 32 {
			sess, sessErr := a.accounts.GetVisitorSession(ctx, id, in.KeyIdle)
			if sessErr == nil && a.deps.VisitorSessionValid(sess, in.SiteID) {
				susp, err := a.accounts.UserSuspended(ctx, sess.UserID)
				if err != nil {
					return anon, err
				}
				if susp {
					return anon, &Error{Code: "account_suspended"}
				}
				if in.CSRFValid {
					_ = a.accounts.TouchVisitorSession(ctx, id, in.SessionIdle)
					if sess.UserID == ownerID {
						return a.visitorSaver(ctx, in, model.Actor{ID: sess.UserID, Kind: "owner"})
					}
					return a.visitorSaver(ctx, in, model.Actor{ID: sess.UserID, Kind: "visitor"})
				}
				if mode == "on" {
					return anon, &Error{Code: "csrf_required"}
				}
			}
		}
	}
	outcome := "allowed"
	if mode == "on" {
		if allowAnon {
			outcome = "overridden"
		} else {
			outcome = "rejected"
		}
	}
	a.deps.VisitorLog(mode, outcome)
	if outcome == "rejected" {
		return anon, &Error{Code: "visitor_auth_required"}
	}
	return a.visitorSaver(ctx, in, anon)
}

func (a *Adapter) visitorSaver(ctx context.Context, in VisitorWriteInput, actor model.Actor) (model.Actor, error) {
	if actor.Kind != "visitor" && actor.Kind != "anonymous" {
		return actor, nil
	}
	ok, err := a.PersonSaverAllowed(ctx, PersonSaverInput{SiteID: in.SiteID, UserID: actor.ID, VisitorPass: in.VisitorPass})
	if err != nil {
		return actor, err
	}
	if !ok {
		return actor, &Error{Code: "not_allowed_to_save"}
	}
	return actor, nil
}

// BoardWriterInput contains request facts for authorizing a board writer.
type BoardWriterInput struct {
	SiteID, Key                                  string
	Preview, ContentHost, CSRFValid, VisitorPass bool
}

// BoardWriterResult contains the writer and any reached rate-limit headers.
type BoardWriterResult struct {
	Actor   model.Actor
	Headers map[string][]string
}

// BoardWriter authorizes a board write and applies its visitor write limits.
func (a *Adapter) BoardWriter(ctx context.Context, in BoardWriterInput) (out BoardWriterResult, err error) {
	if in.Key != "" || a.deps.ContentOwnerBrowser(in.SiteID) {
		ownerID, _, err := siterepo.GetSiteWriteGate(ctx, a.database, in.SiteID)
		if err != nil {
			return out, &Error{Code: "site_not_found"}
		}
		if in.Key != "" {
			u, ok, err := a.deps.VisitorResolveKey(ctx, in.Key)
			if err != nil {
				return out, err
			}
			if !ok {
				return out, &Error{Code: "invalid_api_key"}
			}
			if !u.IsAdmin && u.ID != ownerID {
				return out, &Error{Code: "site_not_found"}
			}
			out.Actor = model.Actor{ID: u.ID, Kind: "owner"}
			if u.ID != ownerID {
				out.Actor.Kind = "admin"
			}
		} else if a.deps.ContentOwnerBrowser(in.SiteID) {
			if !in.CSRFValid {
				return out, &Error{Code: "csrf_required"}
			}
			out.Actor = model.Actor{ID: ownerID, Kind: "owner"}
		} else {
			return out, &Error{Code: "owner_only"}
		}
		out.Actor = a.deps.ContentAuthor(ctx, out.Actor)
		return out, nil
	}
	offline, err := siterepo.SiteOffline(ctx, a.database, in.SiteID)
	if err != nil {
		return out, err
	}
	if offline {
		return out, &Error{Code: "site_offline"}
	}
	if in.Preview {
		return out, &Error{Code: "preview_read_only"}
	}
	if in.ContentHost {
		return out, &Error{Code: "visitor_auth_required"}
	}
	sess := a.deps.CollectionStrictSession(in.SiteID)
	if !sess.OK {
		return out, &Error{Code: "visitor_auth_required"}
	}
	if err := a.CheckVisitorAccess(ctx, VisitorAccessInput{UserID: sess.UserID, SessionID: sess.ID, NeedCSRF: true, CSRFValid: in.CSRFValid, Idle: a.deps.VisitorSessionIdle}); err != nil {
		return out, err
	}
	allowed, err := a.PersonSaverAllowed(ctx, PersonSaverInput{SiteID: in.SiteID, UserID: sess.UserID, VisitorPass: in.VisitorPass})
	if err != nil {
		return out, err
	}
	if !allowed {
		return out, &Error{Code: "not_allowed_to_save"}
	}
	allowed, out.Headers = a.deps.CollectionAppendLimit()
	if !allowed {
		return out, &Error{Code: "rate_limited"}
	}
	if !a.deps.CollectionBoardLimit(sess.UserID) {
		return out, &Error{Code: "rate_limited"}
	}
	out.Actor = a.deps.ContentAuthor(ctx, model.Actor{ID: sess.UserID, Kind: "visitor"})
	return out, nil
}
