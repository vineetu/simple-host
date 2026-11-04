package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	model "github.com/vsriram/simple-host/internal/model"
	siterepo "github.com/vsriram/simple-host/internal/repository/sites"
	storagerules "github.com/vsriram/simple-host/internal/rules/storage"
)

// EntryRequest contains request facts shared by the three item operations.
type EntryRequest struct {
	SiteName, Name, Handle, Key, Method, Path                                      string
	ID                                                                             int64
	ValidID, ValidName, DeclareFirst, ContentHost, CSRFValid, VisitorPass, Preview bool
	EntryMaxKB, BoardMax, BoardItemMaxKB, MaxItems                                 int
	ListMaxBytes, MaxBytes                                                         int64
	Idle                                                                           time.Duration
	Window                                                                         time.Duration
	IfVersion                                                                      int64
	ValidIfVersion                                                                 bool
	ReadPatch                                                                      func(int64) ([]byte, error)
}

// EntryResult contains the completed operation and response-shaping facts.
type EntryResult struct {
	Item     model.CollectionItem
	Settings model.DataSettings
	Route    string
	Done     bool
	Headers  map[string][]string
}

// EntryStageError identifies whether an item operation failed before mutation.
type EntryStageError struct {
	Stage string
	Err   error
}

func (e *EntryStageError) Error() string { return e.Err.Error() }
func (e *EntryStageError) Unwrap() error { return e.Err }

func (a *Adapter) prepareEntry(ctx context.Context, in EntryRequest, operation string) (out EntryResult, siteID string, access VisitorAccessInput, err error) {
	defer func() {
		if err != nil {
			err = &EntryStageError{Stage: "preparation", Err: err}
		}
	}()
	if in.SiteName == "" || !in.ValidName {
		err = &Error{Code: "private_not_found"}
		return
	}
	siteID, err = a.deps.ResolveNamedSite(ctx, in.SiteName)
	if err != nil {
		err = &Error{Code: "private_not_found"}
		return
	}
	out.Settings, err = a.WriteCollectionSettings(ctx, siteID, in.Name, in.DeclareFirst)
	if errors.Is(err, sql.ErrNoRows) {
		err = &Error{Code: "site_not_found"}
	}
	if err != nil {
		return
	}
	if out.Settings.Kind == "mine" {
		err = &Error{Code: "personal_item"}
		return
	}
	out.Route = "visitor"
	if operation == "update" && out.Settings.Kind == "board" {
		out.Route = "board"
	} else if operation != "undo" && (in.Key != "" || a.deps.ContentOwnerBrowser(siteID)) {
		out.Route = "owner"
	} else if out.Settings.Kind == "board" {
		out.Route = "board"
	}
	if out.Route == "owner" {
		return
	}
	err = a.entrySiteAvailable(ctx, siteID, operation != "withdraw" && out.Route != "board")
	if err != nil || out.Route == "board" {
		return
	}
	if in.ContentHost {
		err = &Error{Code: "visitor_auth_required"}
		return
	}
	sess := a.deps.CollectionStrictSession(siteID)
	if !sess.OK {
		err = &Error{Code: "visitor_auth_required"}
		return
	}
	access = VisitorAccessInput{UserID: sess.UserID, SessionID: sess.ID, NeedCSRF: true, CSRFValid: in.CSRFValid, Idle: in.Idle}
	return
}

func (a *Adapter) entrySiteAvailable(ctx context.Context, siteID string, checkOffline bool) error {
	suspended, reason, err := siterepo.SiteSuspension(ctx, a.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "site_not_found"}
	}
	if err != nil {
		return err
	}
	if suspended {
		return &Error{Code: "site_suspended", Message: reason}
	}
	if checkOffline {
		offline, err := siterepo.SiteOffline(ctx, a.database, siteID)
		if err != nil {
			return err
		}
		if offline {
			return &Error{Code: "site_offline"}
		}
	}
	return nil
}

// UpdateEntry resolves, authorizes and completes an item patch.
func (a *Adapter) UpdateEntry(ctx context.Context, in EntryRequest) (out EntryResult, err error) {
	out, siteID, access, err := a.prepareEntry(ctx, in, "update")
	if err != nil {
		return out, err
	}
	if out.Route != "visitor" {
		return a.changeSelectedEntry(ctx, in, out, siteID, "update")
	}
	out.Item, out.Settings, err = a.updateVisitorEntry(ctx, updateVisitorEntryInput{SiteID: siteID, Name: in.Name, VisitorAccess: access, ID: in.ID, ValidID: in.ValidID, DeclareFirst: in.DeclareFirst, VisitorPass: in.VisitorPass, EntryMaxKB: in.EntryMaxKB, ListMaxBytes: in.ListMaxBytes, ReadPatch: in.ReadPatch})
	return out, err
}

// WithdrawEntry resolves, authorizes and completes an item withdrawal.
func (a *Adapter) WithdrawEntry(ctx context.Context, in EntryRequest) (out EntryResult, err error) {
	out, siteID, access, err := a.prepareEntry(ctx, in, "withdraw")
	if err != nil {
		return out, err
	}
	if out.Route != "visitor" {
		return a.changeSelectedEntry(ctx, in, out, siteID, "withdraw")
	}
	out.Settings, err = a.withdrawVisitorEntry(ctx, withdrawVisitorEntryInput{SiteID: siteID, Name: in.Name, VisitorAccess: access, ID: in.ID, ValidID: in.ValidID, DeclareFirst: in.DeclareFirst})
	return out, err
}

// UndoEntry resolves, authorizes and completes restoration of an item.
func (a *Adapter) UndoEntry(ctx context.Context, in EntryRequest) (out EntryResult, err error) {
	out, siteID, access, err := a.prepareEntry(ctx, in, "undo")
	if err != nil {
		return out, err
	}
	if out.Route != "visitor" {
		return a.changeSelectedEntry(ctx, in, out, siteID, "undo")
	}
	out.Item, out.Done, out.Settings, err = a.undoVisitorEntry(ctx, undoVisitorEntryInput{SiteID: siteID, Name: in.Name, VisitorAccess: access, ID: in.ID, Window: in.Window, ValidID: in.ValidID, DeclareFirst: in.DeclareFirst, VisitorPass: in.VisitorPass, MaxItems: in.MaxItems, MaxBytes: in.MaxBytes})
	return out, err
}

func (a *Adapter) entryManager(ctx context.Context, in EntryRequest) (string, error) {
	if in.ContentHost {
		return "", &Error{Code: "private_not_found"}
	}
	if in.Key != "" {
		u, ok, err := a.deps.VisitorResolveKey(ctx, in.Key)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", &Error{Code: "private_not_found"}
		}
		if u.IsAdmin {
			id, err := a.deps.ResolveNamedSite(ctx, in.SiteName)
			if err != nil {
				return "", &Error{Code: "private_not_found"}
			}
			log.Printf("admin_private_edit user_id=%s site_id=%s route=%s %s", u.ID, id, in.Method, in.Path)
			return id, nil
		}
		site, err := siterepo.GetSiteByUser(ctx, a.database, u.ID, in.SiteName)
		if err != nil || (in.Handle != "" && !strings.EqualFold(in.Handle, u.Handle.String)) {
			return "", &Error{Code: "private_not_found"}
		}
		return site.ID, nil
	}
	id, err := a.deps.ResolveNamedSite(ctx, in.SiteName)
	if err != nil {
		return "", &Error{Code: "private_not_found"}
	}
	home, here, err := a.deps.CollectionPrivateHome(id)
	if err != nil {
		return "", err
	}
	if !here {
		return "", &Error{Code: "private_not_found"}
	}
	sess := a.deps.CollectionStrictSession(id)
	if !sess.OK || sess.UserID != home.OwnerID || in.VisitorPass {
		return "", &Error{Code: "private_not_found"}
	}
	_ = a.accounts.TouchVisitorSession(ctx, sess.ID, in.Idle)
	if !in.CSRFValid {
		return "", &Error{Code: "csrf_required"}
	}
	return id, nil
}

func (a *Adapter) changeSelectedEntry(ctx context.Context, in EntryRequest, out EntryResult, siteID, operation string) (_ EntryResult, resultErr error) {
	stage := "preparation"
	defer func() {
		if resultErr != nil {
			resultErr = &EntryStageError{Stage: stage, Err: resultErr}
		}
	}()
	var actor model.Actor
	var err error
	if out.Route == "board" {
		writer, e := a.BoardWriter(ctx, BoardWriterInput{SiteID: siteID, Key: in.Key, Preview: in.Preview, ContentHost: in.ContentHost, CSRFValid: in.CSRFValid, VisitorPass: in.VisitorPass})
		out.Headers, actor = writer.Headers, writer.Actor
		if e != nil {
			return out, e
		}
	} else {
		siteID, err = a.entryManager(ctx, in)
		if err != nil {
			return out, err
		}
		if err = a.entrySiteAvailable(ctx, siteID, false); err != nil {
			return out, err
		}
		set, err := a.collectionSettings(ctx, siteID, in.Name)
		if err != nil {
			return out, err
		}
		if set.Kind == "mine" {
			return out, &Error{Code: "personal_only"}
		}
		if !set.Private && set.Kind != "board" && operation == "update" {
			return out, &Error{Code: "append_only"}
		}
	}
	if !in.ValidID {
		return out, &Error{Code: "selected_not_found"}
	}
	if operation == "update" && out.Route == "board" && !in.ValidIfVersion {
		return out, &Error{Code: "invalid_if_match"}
	}
	if operation == "withdraw" {
		if out.Route == "owner" {
			actor = a.entryManagerActor(ctx, in, siteID)
		}
		stage = "mutation"
		found, err := softDeleteItem(ctx, a.database, siteID, in.Name, in.ID, actor)
		if err == nil && !found {
			err = &Error{Code: "selected_not_found"}
		}
		return out, err
	}
	if operation == "undo" {
		stage = "mutation"
		out.Item, out.Done, err = undoOwnWithdrawal(ctx, a.database, siteID, out.Settings.Name, in.ID, actor.ID, in.Window, true, false, in.BoardMax, actor, in.MaxBytes, true)
		return out, err
	}
	limit := in.ListMaxBytes
	if out.Route == "board" {
		limit = int64(in.BoardItemMaxKB) << 10
	}
	raw, err := in.ReadPatch(limit)
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == "item_too_large" {
			return out, err
		}
		return out, &Error{Code: "selected_invalid_patch"}
	}
	patch, valid := storagerules.DecodeLegacyEntryPatch(raw)
	if !valid {
		return out, &Error{Code: "selected_invalid_patch"}
	}
	if !storagerules.LegacyJSONStorable(raw) {
		return out, &Error{Code: "invalid_json"}
	}
	transform := func(old json.RawMessage) (json.RawMessage, error) {
		// Board edits allow an already oversized item to shrink, as before.
		maxBytes := limit
		if out.Route == "board" && int64(len(old)) > maxBytes {
			maxBytes = int64(len(old))
		}
		next, err := storagerules.PatchLegacyEntry(old, patch, maxBytes)
		if err == nil && out.Route == "owner" && int64(len(next)) > limit {
			return nil, storagerules.ErrLegacyEntryTooLarge
		}
		return next, err
	}
	if out.Route == "board" {
		stage = "mutation"
		out.Item, err = updateItemVersioned(ctx, a.database, siteID, out.Settings.Name, in.ID, in.IfVersion, actor, in.MaxBytes, transform)
	} else {
		actor = a.entryManagerActor(ctx, in, siteID)
		stage = "mutation"
		out.Item, err = updateCollectionItemByID(ctx, a.database, siteID, in.Name, in.ID, actor, in.MaxBytes, transform)
	}
	a.deps.BoundHistory(ctx, siteID)
	return out, err
}

func (a *Adapter) entryManagerActor(ctx context.Context, in EntryRequest, siteID string) model.Actor {
	owner, _, _ := siterepo.GetSiteWriteGate(ctx, a.database, siteID)
	actor := model.Actor{ID: owner, Kind: "owner"}
	if in.Key != "" {
		if u, ok, err := a.deps.VisitorResolveKey(ctx, in.Key); err == nil && ok {
			actor.ID = u.ID
			if u.IsAdmin && u.ID != owner {
				actor.Kind = "admin"
			}
		}
	}
	return a.deps.ContentAuthor(ctx, actor)
}
