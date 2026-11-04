package legacy

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"

	model "github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/legacy"
	siterepo "github.com/vsriram/simple-host/internal/repository/sites"
)

// GetPersonalInput identifies a personal read and its request facts.
type GetPersonalInput struct {
	SiteID, Name                 string
	OwnerKey, Count, ContentHost bool
}

// GetPersonalResult contains a visitor's record or an owner's coarse count.
type GetPersonalResult struct {
	Item                model.CollectionItem
	Found, Counted, Few bool
	Count               int64
}

// GetPersonal authorizes and reads a personal record or its owner-visible count.
func (a *Adapter) GetPersonal(ctx context.Context, in GetPersonalInput) (out GetPersonalResult, err error) {
	if in.OwnerKey || a.deps.CollectionOwnerBrowser(in.SiteID) {
		if in.Count {
			n, err := repo.CountLiveItems(ctx, a.database, in.SiteID, in.Name)
			if err != nil {
				return out, &Error{Code: "internal"}
			}
			out.Counted, out.Count = true, n
			if n > 0 && n < repo.PersonalFewMax {
				out.Count, out.Few = 0, true
			}
			return out, nil
		}
		if in.OwnerKey {
			return out, &Error{Code: "personal_data"}
		}
	}
	if in.ContentHost {
		return out, &Error{Code: "visitor_auth_required"}
	}
	sess := a.deps.CollectionStrictSession(in.SiteID)
	if !sess.OK {
		return out, &Error{Code: "visitor_auth_required"}
	}
	suspended, err := a.accounts.UserSuspended(ctx, sess.UserID)
	if err != nil {
		return out, &Error{Code: "internal"}
	}
	if suspended {
		return out, &Error{Code: "account_suspended"}
	}
	_ = a.accounts.TouchVisitorSession(ctx, sess.ID, a.deps.VisitorSessionIdle)
	out.Item, out.Found, err = repo.GetPersonal(ctx, a.database, in.SiteID, in.Name, sess.UserID)
	return out, err
}

// PersonalPatchError describes a personal-document transformation refusal.
type PersonalPatchError struct{ Kind, Message, WireCode string }

func (e *PersonalPatchError) Error() string { return e.Message }

// SavePersonalInput identifies a personal write and its idempotency scope.
type SavePersonalInput struct {
	SiteID, Name, Route, IdempotencyKey string
	Actor                               model.Actor
	Body                                []byte
	SuccessStatus                       int
}

// SavePersonalResult contains a saved record or a replayed record.
type SavePersonalResult struct {
	Item            model.CollectionItem
	Found, Replayed bool
	ReplayStatus    int
	Headers         map[string][]string
}

// savePersonal claims idempotency, saves a visitor record and bounds history.
func (a *Adapter) savePersonal(ctx context.Context, in SavePersonalInput) (out SavePersonalResult, err error) {
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 255 {
		return out, &Error{Code: "invalid_idempotency_key"}
	}
	var scope []byte
	saved := false
	if key != "" && in.Actor.ID != "" {
		var prev model.LegacyIdempotentResponse
		var state string
		scope, prev, state, err = a.ClaimWrite(ctx, in.SiteID, in.Route, in.Actor.ID, key, in.Body)
		if err != nil {
			return out, &Error{Code: "idempotency_internal"}
		}
		switch state {
		case "reused":
			return out, &Error{Code: "idempotency_key_reused"}
		case "pending":
			return out, &Error{Code: "idempotency_in_progress"}
		case "replay":
			out.Replayed, out.ReplayStatus = true, prev.Status
			out.Item, out.Found, err = repo.GetPersonal(ctx, a.database, in.SiteID, in.Name, in.Actor.ID)
			return out, err
		}
	}
	defer func() {
		if scope != nil && !saved {
			if err := a.ReleaseIdempotencyKey(context.WithoutCancel(ctx), scope); err != nil {
				log.Printf("idempotency release: %v", err)
			}
		}
	}()
	out.Item, err = savePersonal(ctx, a.database, in.SiteID, in.Name, in.Actor, a.deps.PersonalSiteMaxBytes(), a.deps.PersonalPeopleMax(), a.deps.PersonalNext)
	a.deps.BoundPersonalHistory(ctx, in.SiteID)
	if err != nil {
		return out, err
	}
	out.Found = true
	if scope != nil {
		saved = true
		if err := a.SaveIdempotentResponse(context.WithoutCancel(ctx), scope, in.SuccessStatus, "", out.Item.ID); err != nil {
			log.Printf("idempotency save: %v", err)
		}
	}
	return out, nil
}

// PutPersonalInput contains the request facts for a personal replacement.
type PutPersonalInput struct {
	SiteID, Name, Key, IdempotencyKey       string
	ContentHost, Preview, VisitorPass, CSRF bool
	SuccessStatus                           int
	DecodeBody                              func() (CollectionBody, error)
}

// PutPersonal authorizes a visitor and replaces that visitor's personal record.
func (a *Adapter) PutPersonal(ctx context.Context, in PutPersonalInput) (out SavePersonalResult, err error) {
	if in.Key != "" {
		return out, &Error{Code: "personal_data"}
	}
	offline, err := siterepo.SiteOffline(ctx, a.database, in.SiteID)
	if err != nil {
		return out, &Error{Code: "internal"}
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
	suspended, err := a.accounts.UserSuspended(ctx, sess.UserID)
	if err != nil {
		return out, &Error{Code: "internal"}
	}
	if suspended {
		return out, &Error{Code: "account_suspended"}
	}
	if !in.CSRF {
		return out, &Error{Code: "csrf_required"}
	}
	_ = a.accounts.TouchVisitorSession(ctx, sess.ID, a.deps.VisitorSessionIdle)
	actor := model.Actor{ID: sess.UserID, Kind: "visitor"}
	if err := a.authorizeCollectionSaver(ctx, in.SiteID, actor, in.VisitorPass); err != nil {
		return out, err
	}
	var allowed bool
	allowed, out.Headers = a.deps.CollectionAppendLimit()
	if !allowed {
		return out, &Error{Code: "rate_limited"}
	}
	if email, err := a.collectionVisitorEmail(ctx, actor.ID); err == nil {
		actor.Email = email
	}
	decoded, err := in.DecodeBody()
	if err != nil {
		return out, err
	}
	raw := decoded.Raw
	doc := json.RawMessage(bytes.TrimSpace(raw))
	local := *a
	local.deps.PersonalNext = func(json.RawMessage) (json.RawMessage, error) { return doc, nil }
	result, err := local.savePersonal(ctx, SavePersonalInput{SiteID: in.SiteID, Name: in.Name, Route: "PUT data/" + in.Name, Actor: actor, Body: raw, IdempotencyKey: in.IdempotencyKey, SuccessStatus: in.SuccessStatus})
	result.Headers = out.Headers
	return result, err
}

// DeletePersonalInput identifies the visitor's personal record to delete.
type DeletePersonalInput struct {
	SiteID, Name string
	Actor        model.Actor
}

// DeletePersonal deletes a live record or forgets one taken by an owner clear.
func (a *Adapter) deletePersonal(ctx context.Context, in DeletePersonalInput) (bool, error) {
	it, found, err := repo.GetPersonal(ctx, a.database, in.SiteID, in.Name, in.Actor.ID)
	if err == nil && found {
		found, err = softDeleteItem(ctx, a.database, in.SiteID, in.Name, it.ID, in.Actor)
	}
	// A person's delete still counts if the owner's clear took their record.
	if err == nil && !found {
		found, err = forgetClearedPersonal(ctx, a.database, in.SiteID, in.Name, in.Actor)
	}
	return found, err
}

// DeletePersonalRequest contains the request facts for a personal deletion.
type DeletePersonalRequest struct {
	SiteName, Name string
	DeclareFirst   bool
	ActorFacts     CollectionWriteActor
}

// DeletePersonalResult contains the reached headers and deletion outcome.
type DeletePersonalResult struct {
	Headers map[string][]string
	Found   bool
	Kind    string
}

// DeletePersonalRequest deletes the visitor's own record after resolving its target.
func (a *Adapter) personalTarget(ctx context.Context, in DeletePersonalRequest) (out DeletePersonalResult, siteID string, err error) {
	gate := CollectionGateInput{SiteName: in.SiteName, Collection: in.Name}
	if gate.SiteName != "" && ValidCollectionName(gate.Collection) {
		gate.OriginAllowed, out.Headers = a.deps.CollectionOrigin(gate.SiteName)
	}
	if err := a.AuthorizeCollectionGate(ctx, gate); err != nil {
		return out, "", err
	}
	siteID, err = a.deps.ResolveNamedSite(ctx, in.SiteName)
	if err != nil {
		return out, "", &Error{Code: "site_not_found"}
	}
	suspended, reason, err := siterepo.SiteSuspension(ctx, a.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, "", &Error{Code: "site_not_found"}
	}
	if err != nil {
		return out, "", &Error{Code: "internal"}
	}
	if suspended {
		return out, "", &Error{Code: "site_suspended", Message: reason}
	}
	offline, err := siterepo.SiteOffline(ctx, a.database, siteID)
	if err != nil {
		return out, "", &Error{Code: "internal"}
	}
	if offline {
		return out, "", &Error{Code: "site_offline"}
	}
	set, err := a.WriteCollectionSettings(ctx, siteID, in.Name, in.DeclareFirst)
	if errors.Is(err, sql.ErrNoRows) {
		return out, "", &Error{Code: "site_not_found"}
	}
	if err != nil {
		return out, "", &Error{Code: "internal"}
	}
	out.Kind = set.Kind
	if set.Kind != "mine" {
		return out, "", &Error{Code: "personal_wrong_kind"}
	}
	return out, siteID, nil
}

func (a *Adapter) personalWriter(ctx context.Context, siteID string, facts CollectionWriteActor, needSaver bool) (actor model.Actor, headers map[string][]string, err error) {
	if facts.Key != "" {
		return actor, nil, &Error{Code: "personal_data"}
	}
	offline, err := siterepo.SiteOffline(ctx, a.database, siteID)
	if err != nil {
		return actor, nil, &Error{Code: "internal"}
	}
	if offline {
		return actor, nil, &Error{Code: "site_offline"}
	}
	if facts.Preview {
		return actor, nil, &Error{Code: "preview_read_only"}
	}
	if facts.ContentHost {
		return actor, nil, &Error{Code: "visitor_auth_required"}
	}
	sess := a.deps.CollectionStrictSession(siteID)
	if !sess.OK {
		return actor, nil, &Error{Code: "visitor_auth_required"}
	}
	suspended, err := a.accounts.UserSuspended(ctx, sess.UserID)
	if err != nil {
		return actor, nil, &Error{Code: "internal"}
	}
	if suspended {
		return actor, nil, &Error{Code: "account_suspended"}
	}
	if !facts.CSRF {
		return actor, nil, &Error{Code: "csrf_required"}
	}
	_ = a.accounts.TouchVisitorSession(ctx, sess.ID, a.deps.VisitorSessionIdle)
	actor = model.Actor{ID: sess.UserID, Kind: "visitor"}
	if needSaver {
		if err := a.authorizeCollectionSaver(ctx, siteID, actor, facts.VisitorPass); err != nil {
			return actor, nil, err
		}
	}
	allowed, headers := a.deps.CollectionAppendLimit()
	if !allowed {
		return actor, headers, &Error{Code: "rate_limited"}
	}
	if email, err := a.collectionVisitorEmail(ctx, actor.ID); err == nil {
		actor.Email = email
	}
	return actor, headers, nil
}

func mergePersonalHeaders(out *DeletePersonalResult, headers map[string][]string) {
	if out.Headers == nil {
		out.Headers = map[string][]string{}
	}
	for k, v := range headers {
		out.Headers[k] = v
	}
}

// DeletePersonalRequest deletes the visitor's own record after resolving its target.
func (a *Adapter) DeletePersonalRequest(ctx context.Context, in DeletePersonalRequest) (out DeletePersonalResult, err error) {
	out, siteID, err := a.personalTarget(ctx, in)
	if err != nil {
		return out, err
	}
	actor, headers, err := a.personalWriter(ctx, siteID, in.ActorFacts, false)
	mergePersonalHeaders(&out, headers)
	if err != nil {
		return out, err
	}
	out.Found, err = a.deletePersonal(ctx, DeletePersonalInput{SiteID: siteID, Name: in.Name, Actor: actor})
	return out, err
}

// PatchPersonalRequest contains request facts for a personal patch.
type PatchPersonalRequest struct {
	DeletePersonalRequest
	IdempotencyKey string
	SuccessStatus  int
	ReadBody       func() ([]byte, bool, error)
}

// PatchPersonalResult contains reached headers and the save outcome.
type PatchPersonalResult struct {
	DeletePersonalResult
	SavePersonalResult
	SiteID string
}

// PatchPersonal resolves, authorizes and applies a visitor's personal patch.
func (a *Adapter) PatchPersonal(ctx context.Context, in PatchPersonalRequest) (out PatchPersonalResult, err error) {
	out.DeletePersonalResult, out.SiteID, err = a.personalTarget(ctx, in.DeletePersonalRequest)
	if err != nil {
		return out, err
	}
	actor, headers, err := a.personalWriter(ctx, out.SiteID, in.ActorFacts, true)
	mergePersonalHeaders(&out.DeletePersonalResult, headers)
	if err != nil {
		return out, err
	}
	body, tooLarge, err := in.ReadBody()
	if err != nil {
		if tooLarge {
			return out, &Error{Code: "patch_body_limit"}
		}
		return out, &Error{Code: "patch_body_too_large"}
	}
	if !a.deps.CollectionJSONStorable(body) {
		return out, &Error{Code: "invalid_json", Message: `the JSON holds text that cannot be saved: a NUL character (\u0000), half of a surrogate pair (\ud800 alone) or bytes that are not UTF-8. Remove it and send again`}
	}
	next, err := a.deps.PersonalPreparePatch(body)
	if err != nil {
		return out, err
	}
	local := *a
	local.deps.PersonalNext = next
	out.SavePersonalResult, err = local.savePersonal(ctx, SavePersonalInput{SiteID: out.SiteID, Name: in.Name, Route: "PATCH data/" + in.Name, Actor: actor, Body: body, IdempotencyKey: in.IdempotencyKey, SuccessStatus: in.SuccessStatus})
	return out, err
}

// PersonalHistoryRequest contains request facts and a history page or change ID.
type PersonalHistoryRequest struct {
	DeletePersonalRequest
	Limit            int
	Before, ChangeID int64
	ValidID          bool
}

// PersonalHistoryResult contains reached headers and the requested history data.
type PersonalHistoryResult struct {
	DeletePersonalResult
	Entries    []model.HistoryEntry
	Entry      model.HistoryEntry
	Item       model.CollectionItem
	RestoredID int64
}

func (a *Adapter) personalReader(ctx context.Context, siteID string, facts CollectionWriteActor) (actor model.Actor, headers map[string][]string, err error) {
	allowed, headers := a.deps.CollectionReadLimit(siteID)
	if !allowed {
		return actor, headers, &Error{Code: "rate_limited"}
	}
	if facts.ContentHost {
		return actor, headers, &Error{Code: "visitor_auth_required"}
	}
	sess := a.deps.CollectionStrictSession(siteID)
	if !sess.OK {
		return actor, headers, &Error{Code: "visitor_auth_required"}
	}
	suspended, err := a.accounts.UserSuspended(ctx, sess.UserID)
	if err != nil {
		return actor, headers, &Error{Code: "internal"}
	}
	if suspended {
		return actor, headers, &Error{Code: "account_suspended"}
	}
	_ = a.accounts.TouchVisitorSession(ctx, sess.ID, a.deps.VisitorSessionIdle)
	return model.Actor{ID: sess.UserID, Kind: "visitor"}, headers, nil
}

// ReadPersonalHistory resolves and reads the visitor's personal history page.
func (a *Adapter) ReadPersonalHistory(ctx context.Context, in PersonalHistoryRequest) (out PersonalHistoryResult, err error) {
	var siteID string
	out.DeletePersonalResult, siteID, err = a.personalTarget(ctx, in.DeletePersonalRequest)
	if err != nil {
		return out, err
	}
	actor, headers, err := a.personalReader(ctx, siteID, in.ActorFacts)
	mergePersonalHeaders(&out.DeletePersonalResult, headers)
	if err != nil {
		return out, err
	}
	out.Entries, err = a.personalHistory(ctx, PersonalHistoryInput{SiteID: siteID, Name: in.Name, UserID: actor.ID, Limit: in.Limit, Before: in.Before})
	return out, err
}

// ReadPersonalHistoryEntry resolves and reads one personal history change.
func (a *Adapter) ReadPersonalHistoryEntry(ctx context.Context, in PersonalHistoryRequest) (out PersonalHistoryResult, err error) {
	var siteID string
	out.DeletePersonalResult, siteID, err = a.personalTarget(ctx, in.DeletePersonalRequest)
	if err != nil {
		return out, err
	}
	actor, headers, err := a.personalReader(ctx, siteID, in.ActorFacts)
	mergePersonalHeaders(&out.DeletePersonalResult, headers)
	if err != nil {
		return out, err
	}
	out.Entry, err = a.personalHistoryEntry(ctx, PersonalHistoryEntryInput{SiteID: siteID, Name: in.Name, Actor: actor, ChangeID: in.ChangeID, ValidID: in.ValidID})
	return out, err
}

// RestorePersonalRequest resolves and restores a visitor's history change.
func (a *Adapter) RestorePersonalRequest(ctx context.Context, in PersonalHistoryRequest) (out PersonalHistoryResult, err error) {
	var siteID string
	out.DeletePersonalResult, siteID, err = a.personalTarget(ctx, in.DeletePersonalRequest)
	if err != nil {
		return out, err
	}
	actor, headers, err := a.personalWriter(ctx, siteID, in.ActorFacts, true)
	mergePersonalHeaders(&out.DeletePersonalResult, headers)
	if err != nil {
		return out, err
	}
	out.Item, out.RestoredID, err = a.restorePersonal(ctx, PersonalHistoryEntryInput{SiteID: siteID, Name: in.Name, Actor: actor, ChangeID: in.ChangeID, ValidID: in.ValidID})
	return out, err
}

// PersonalHistoryInput identifies a visitor's personal record and history page.
type PersonalHistoryInput struct {
	SiteID, Name, UserID string
	Limit                int
	Before               int64
}

// PersonalHistory returns the history of a visitor's personal record.
func (a *Adapter) personalHistory(ctx context.Context, in PersonalHistoryInput) ([]model.HistoryEntry, error) {
	itemID, err := repo.PersonalItemID(ctx, a.database, in.SiteID, in.Name, in.UserID)
	if err != nil {
		return nil, err
	}
	entries := []model.HistoryEntry{}
	if itemID != 0 {
		entries, err = repo.ListItemHistory(ctx, a.database, in.SiteID, in.Name, itemID, in.Limit, in.Before)
	}
	return entries, err
}

// PersonalHistoryEntryInput identifies a change belonging to a visitor's record.
type PersonalHistoryEntryInput struct {
	SiteID, Name string
	Actor        model.Actor
	ChangeID     int64
	ValidID      bool
}

func (a *Adapter) personalHistoryEntry(ctx context.Context, in PersonalHistoryEntryInput) (model.HistoryEntry, error) {
	itemID, err := repo.PersonalItemID(ctx, a.database, in.SiteID, in.Name, in.Actor.ID)
	if err != nil {
		return model.HistoryEntry{}, &Error{Code: "internal"}
	}
	if !in.ValidID || itemID == 0 {
		return model.HistoryEntry{}, &Error{Code: "history_not_found"}
	}
	e, err := readHistoryEntry(ctx, a.database, in.SiteID, "list", in.Name, in.ChangeID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return e, &Error{Code: "internal"}
	}
	if err != nil || e.ItemID == nil || *e.ItemID != itemID {
		return e, &Error{Code: "history_not_found"}
	}
	return e, nil
}

// RestorePersonal restores a visitor's record to the value before a change.
func (a *Adapter) restorePersonal(ctx context.Context, in PersonalHistoryEntryInput) (model.CollectionItem, int64, error) {
	e, err := a.personalHistoryEntry(ctx, in)
	if err != nil {
		return model.CollectionItem{}, 0, err
	}
	it, err := restoreItemVersion(ctx, a.database, in.SiteID, in.Name, e.ID, in.Actor, a.deps.PersonalSiteMaxBytes(), 0)
	a.deps.BoundPersonalHistory(ctx, in.SiteID)
	return it, e.ID, err
}
