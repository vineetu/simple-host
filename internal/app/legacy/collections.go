package legacy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"iter"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/repository/legacy"
)

// CollectionOptionsInput contains the origin facts for a preflight request.
type CollectionOptionsInput struct {
	SiteName      string
	OriginAllowed bool
}

// CollectionOptions authorizes a collection preflight request.
func (a *Adapter) CollectionOptions(_ context.Context, in CollectionOptionsInput) error {
	name := strings.TrimSpace(in.SiteName)
	if name == "" || !in.OriginAllowed {
		return &Error{Code: "origin_not_allowed", Message: "origin not allowed"}
	}
	return nil
}

var collectionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidCollectionName reports whether a collection name is valid.
func ValidCollectionName(name string) bool { return collectionNamePattern.MatchString(name) }

// CollectionGateInput contains the request facts for a collection origin gate.
type CollectionGateInput struct {
	SiteName, Collection string
	OriginAllowed        bool
	KeyWithoutPage       bool
	DeclareFirst         bool
}

// AuthorizeCollectionGate validates and authorizes a collection request.
func (a *Adapter) AuthorizeCollectionGate(_ context.Context, in CollectionGateInput) error {
	if err := validateCollectionTarget(in.SiteName, in.Collection); err != nil {
		if in.KeyWithoutPage {
			return &Error{Code: "invalid_site_or_collection", Message: "invalid site or collection name"}
		}
		return err
	}
	if !in.KeyWithoutPage && !in.OriginAllowed {
		return &Error{Code: "origin_not_allowed", Message: "origin not allowed"}
	}
	return nil
}

// prepareCollectionAppend applies the write gate and reads the declaration.
func (a *Adapter) prepareCollectionAppend(ctx context.Context, in CollectionGateInput) (string, db.DataSettings, error) {
	if err := a.AuthorizeCollectionGate(ctx, in); err != nil {
		return "", db.DataSettings{}, err
	}
	id, err := a.deps.ResolveWriteSite(ctx, in.SiteName)
	if err != nil {
		return "", db.DataSettings{}, err
	}
	set, err := a.WriteCollectionSettings(ctx, id, in.Collection, in.DeclareFirst)
	if err == nil {
		switch {
		case set.Kind == db.KindContent:
			err = &Error{Code: "append_content"}
		case set.Kind == db.KindPersonal:
			err = &Error{Code: "append_personal"}
		case set.Kind == "" && !set.Shared:
			err = &Error{Code: "declare_first"}
		}
	}
	return id, set, err
}

// authorizeCollectionSubmitter applies the declared-submission identity rule.
func (a *Adapter) authorizeCollectionSubmitter(_ context.Context, set db.DataSettings, actor db.Actor) error {
	if set.Kind == db.KindEntries && actor.Kind == "anonymous" {
		return &Error{Code: "visitor_auth_required"}
	}
	return nil
}

func validateCollectionTarget(siteName, collection string) error {
	if siteName == "" {
		return &Error{Code: "site_name_required", Message: "site name is required"}
	}
	if !ValidCollectionName(collection) {
		return &Error{Code: "invalid_collection", Message: "invalid collection name"}
	}
	return nil
}

// collectionPage parses the legacy collection pagination parameters.
func collectionPage(limitValue, beforeValue string) (int, int64) {
	limit := 50
	if v, err := strconv.Atoi(limitValue); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}
	var before int64
	if v, err := strconv.ParseInt(beforeValue, 10, 64); err == nil && v > 0 {
		before = v
	}
	return limit, before
}

// collectionSettings reads the declaration for a collection.
func (a *Adapter) collectionSettings(ctx context.Context, siteID, coll string) (db.DataSettings, error) {
	return legacy.GetDataSettings(ctx, a.database, siteID, coll)
}

// WriteCollectionSettings applies the configured default to an undeclared name.
func (a *Adapter) WriteCollectionSettings(ctx context.Context, siteID, coll string, declareFirst bool) (db.DataSettings, error) {
	set, err := a.collectionSettings(ctx, siteID, coll)
	if err == nil && !declareFirst {
		set.Shared = true
	}
	return set, err
}

// EntryMaxBytes selects the item limit for a collection declaration.
func EntryMaxBytes(set db.DataSettings, entryMaxKB int, listMaxBytes int64) int64 {
	if set.Kind == db.KindEntries {
		return int64(entryMaxKB) << 10
	}
	return listMaxBytes
}

// ErrPersonalList indicates that personal records cannot be read as a list.
var ErrPersonalList = errors.New("personal records cannot be read as a list")

// CollectionReadSettingsInput identifies the list and whether its owner is reading.
type CollectionReadSettingsInput struct {
	SiteID, Collection string
	Owner              bool
}

// CollectionReadSettings checks site availability and reads the list declaration.
func (a *Adapter) collectionReadSettings(ctx context.Context, in CollectionReadSettingsInput) (db.DataSettings, error) {
	if !in.Owner {
		suspended, reason, err := db.SiteSuspension(ctx, a.database, in.SiteID)
		if errors.Is(err, sql.ErrNoRows) {
			return db.DataSettings{}, &Error{Code: "site_not_found"}
		}
		if err != nil {
			return db.DataSettings{}, &Error{Code: "internal"}
		}
		if suspended {
			return db.DataSettings{}, &Error{Code: "site_suspended", Message: reason}
		}
		offline, err := db.SiteOffline(ctx, a.database, in.SiteID)
		if err != nil {
			return db.DataSettings{}, &Error{Code: "internal"}
		}
		if offline {
			return db.DataSettings{}, &Error{Code: "site_offline"}
		}
	}
	set, err := a.collectionSettings(ctx, in.SiteID, in.Collection)
	if err != nil {
		return set, &Error{Code: "internal"}
	}
	if set.Kind == db.KindPersonal {
		return set, ErrPersonalList
	}
	return set, nil
}

// CollectionBrowserSession contains the visitor session facts for a list read.
type CollectionBrowserSession struct {
	UserID string
	ID     []byte
	OK     bool
}

// CollectionReadViewInput contains the request facts used to authorize and select a list view.
type CollectionReadViewInput struct {
	SiteID                                                                  string
	OwnerKey, SharedHost, Originless, VisitorPass, HasStrictCookie, Private bool
}

// CollectionReadView authorizes private reads and selects the author view.
func (a *Adapter) collectionReadView(ctx context.Context, in CollectionReadViewInput) (bool, error) {
	if in.Private {
		if in.SharedHost || (!in.OwnerKey && in.Originless) {
			return false, &Error{Code: "private_not_found"}
		}
		if !in.OwnerKey {
			ownerID, here, err := a.deps.CollectionOwnerDomain(ctx, in.SiteID)
			if err != nil {
				return false, &Error{Code: "internal"}
			}
			if !here {
				return false, &Error{Code: "private_not_found"}
			}
			sess := a.deps.CollectionBrowserSession(ctx, in.SiteID)
			if !sess.OK || sess.UserID != ownerID || in.VisitorPass {
				return false, &Error{Code: "private_not_found"}
			}
			_ = db.TouchVisitorSession(ctx, a.database, sess.ID, a.deps.VisitorSessionIdle)
		}
	}
	if in.OwnerKey {
		return true, nil
	}
	if in.VisitorPass || !in.HasStrictCookie || in.SharedHost {
		return false, nil
	}
	sess := a.deps.CollectionBrowserSession(ctx, in.SiteID)
	if !sess.OK {
		return false, nil
	}
	ownerID, here, err := a.deps.CollectionOwnerDomain(ctx, in.SiteID)
	return err == nil && here && sess.UserID == ownerID, nil
}

// CollectionListInput contains the resolved values for a collection page.
type CollectionListInput struct {
	SiteID, Collection, Limit, Before string
	WithAuthor                        bool
	Settings                          db.DataSettings
}

// ListCollection reads and shapes a collection page for its caller.
func (a *Adapter) listCollection(ctx context.Context, in CollectionListInput) ([]db.CollectionItem, *int64, error) {
	siteID, coll, withAuthor, set := in.SiteID, in.Collection, in.WithAuthor, in.Settings
	limit, before := collectionPage(in.Limit, in.Before)
	items, err := legacy.ListCollectionItemsByID(ctx, a.database, siteID, coll, limit, before, withAuthor)
	if err != nil {
		return nil, nil, err
	}
	var next *int64
	if len(items) == limit {
		n := items[len(items)-1].ID
		next = &n
	}
	for i := range items {
		if !set.Private && !withAuthor {
			items[i].Data = WithoutSubmitter(items[i].Data)
		}
		if set.Kind != db.KindBoard {
			items[i].Version = 0
		}
	}
	return items, next, nil
}

// CollectionPageInput contains the resolved site and request facts for a list page.
type CollectionPageInput struct {
	CollectionListInput
	CollectionReadViewInput
}

// CollectionPageResult records the headers reached while loading a list page.
type CollectionPageResult struct {
	Items           []db.CollectionItem
	Next            *int64
	Settings        db.DataSettings
	Private, Author bool
}

// CollectionPage checks availability and access before reading a collection page.
func (a *Adapter) collectionPage(ctx context.Context, in CollectionPageInput) (result CollectionPageResult, err error) {
	set, err := a.collectionReadSettings(ctx, CollectionReadSettingsInput{SiteID: in.CollectionListInput.SiteID, Collection: in.Collection, Owner: in.OwnerKey})
	if err != nil {
		return result, err
	}
	result.Settings, result.Private = set, set.Private
	in.CollectionReadViewInput.Private = set.Private
	result.Author, err = a.collectionReadView(ctx, in.CollectionReadViewInput)
	if err != nil {
		return result, err
	}
	in.CollectionListInput.WithAuthor, in.CollectionListInput.Settings = result.Author, set
	result.Items, result.Next, err = a.listCollection(ctx, in.CollectionListInput)
	return result, err
}

// ReadCollectionInput contains the request facts for a collection read.
type ReadCollectionInput struct {
	CollectionListSiteInput
	CollectionReadViewInput
	Limit, Before string
}

// ReadCollectionResult contains the page and headers reached during a read.
type ReadCollectionResult struct {
	CollectionPageResult
	Headers map[string][]string
}

// ReadCollection resolves, authorizes and loads a collection page.
func (a *Adapter) ReadCollection(ctx context.Context, in ReadCollectionInput) (out ReadCollectionResult, err error) {
	out.Headers = make(map[string][]string)
	siteID, owner, err := a.collectionReadTarget(ctx, in.CollectionListSiteInput)
	if err != nil {
		return out, err
	}
	if !owner && !in.CollectionListSiteInput.Originless {
		var headers map[string][]string
		in.CollectionListSiteInput.OriginAllowed, headers = a.deps.CollectionOrigin(in.SiteName)
		for k, v := range headers {
			out.Headers[k] = v
		}
		siteID, err = a.resolveOriginCollection(ctx, in.CollectionListSiteInput)
		if err != nil {
			return out, err
		}
	}
	allowed, headers := a.deps.CollectionReadLimit(siteID)
	for k, v := range headers {
		out.Headers[k] = v
	}
	if !allowed {
		return out, &Error{Code: "rate_limited"}
	}
	in.CollectionReadViewInput.SiteID, in.CollectionReadViewInput.OwnerKey = siteID, owner
	out.CollectionPageResult, err = a.collectionPage(ctx, CollectionPageInput{
		CollectionListInput:     CollectionListInput{SiteID: siteID, Collection: in.Collection, Limit: in.Limit, Before: in.Before},
		CollectionReadViewInput: in.CollectionReadViewInput,
	})
	return out, err
}

// WithoutSubmitter removes the private submitter stamp from an item.
func WithoutSubmitter(data json.RawMessage) json.RawMessage {
	if !bytes.Contains(data, []byte(`"_submitted_by"`)) {
		return data
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return data
	}
	if _, ok := fields["_submitted_by"]; !ok {
		return data
	}
	delete(fields, "_submitted_by")
	out, err := json.Marshal(fields)
	if err != nil {
		return data
	}
	return out
}

// OwnedSiteInput identifies the caller and site for an owner lookup.
type OwnedSiteInput struct {
	SiteName, UserID, ScopedSiteID string
	SignedIn, Admin, Scoped        bool
}

// OwnedSiteID resolves a site accessible to its owner or an administrator.
func (a *Adapter) OwnedSiteID(ctx context.Context, in OwnedSiteInput) (string, error) {
	if !in.SignedIn {
		return "", sql.ErrNoRows
	}
	var site db.Site
	err := sql.ErrNoRows
	if in.UserID != "" {
		site, err = db.GetSiteByUser(ctx, a.database, in.UserID, in.SiteName)
		if err == nil && in.Scoped && in.ScopedSiteID != site.ID {
			err = sql.ErrNoRows
		}
	}
	if err == nil {
		return site.ID, nil
	}
	if errors.Is(err, sql.ErrNoRows) && in.Admin {
		return db.GetSiteIDByName(ctx, a.database, in.SiteName)
	}
	return "", err
}

// OwnerKeyInput contains the request facts used to resolve an owner key.
type OwnerKeyInput struct {
	SiteName, APIKey, Handle, ScopedSiteID string
	Scoped                                 bool
}

// OwnerSiteFromKey resolves a site only when the key can inspect it.
func (a *Adapter) OwnerSiteFromKey(ctx context.Context, in OwnerKeyInput) (string, bool) {
	in.Handle = strings.TrimSpace(in.Handle)
	if in.SiteName == "" || in.APIKey == "" {
		return "", false
	}
	var user db.User
	if a.deps.AdminAPIKey != "" && subtle.ConstantTimeCompare([]byte(in.APIKey), []byte(a.deps.AdminAPIKey)) == 1 {
		user.IsAdmin = true
	} else {
		var err error
		user, err = db.GetUserByAPIKey(ctx, a.database, in.APIKey, a.deps.KeyIdleExpiry)
		if err != nil {
			return "", false
		}
	}
	resolveNamed := in.Handle != "" && user.IsAdmin && (user.ID == "" || !strings.EqualFold(in.Handle, user.Handle.String))
	if in.Handle != "" && !user.IsAdmin && !strings.EqualFold(in.Handle, user.Handle.String) {
		return "", false
	}
	var id string
	var err error
	if resolveNamed {
		id, err = a.deps.ResolveNamedSite(ctx, in.SiteName)
	} else {
		id, err = a.OwnedSiteID(ctx, OwnedSiteInput{SiteName: in.SiteName, UserID: user.ID, SignedIn: true, Admin: user.IsAdmin, ScopedSiteID: in.ScopedSiteID, Scoped: in.Scoped})
	}
	return id, err == nil
}

// CollectionListSiteInput contains the request facts needed to select a list's site.
type CollectionListSiteInput struct {
	OwnerKeyInput
	Collection                string
	Originless, OriginAllowed bool
}

// CollectionReadTarget validates a list target and checks for an owner key.
func (a *Adapter) collectionReadTarget(ctx context.Context, in CollectionListSiteInput) (string, bool, error) {
	if err := validateCollectionTarget(in.SiteName, in.Collection); err != nil {
		return "", false, err
	}
	id, owner := a.OwnerSiteFromKey(ctx, in.OwnerKeyInput)
	if !owner && in.Originless {
		id, err := a.resolveScopedCollectionSite(ctx, in)
		return id, false, err
	}
	if owner && in.Scoped && in.ScopedSiteID != id {
		return "", false, &Error{Code: "site_not_found"}
	}
	return id, owner, nil
}

// ResolveOriginCollection authorizes an origin and resolves the list's site.
func (a *Adapter) resolveOriginCollection(ctx context.Context, in CollectionListSiteInput) (string, error) {
	if err := a.AuthorizeCollectionGate(ctx, CollectionGateInput{SiteName: in.SiteName, Collection: in.Collection, OriginAllowed: in.OriginAllowed}); err != nil {
		return "", err
	}
	return a.resolveScopedCollectionSite(ctx, in)
}

func (a *Adapter) resolveScopedCollectionSite(ctx context.Context, in CollectionListSiteInput) (string, error) {
	id, err := a.deps.ResolveNamedSite(ctx, in.SiteName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &Error{Code: "site_not_found"}
	}
	if err != nil {
		return "", &Error{Code: "internal"}
	}
	if in.Scoped && in.ScopedSiteID != id {
		return "", &Error{Code: "site_not_found"}
	}
	return id, nil
}

// Error describes a legacy operation failure without transport status codes.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

// ListSiteCollectionsInput contains the actor and site for an owner inventory.
type ListSiteCollectionsInput struct {
	SiteName, UserID, ScopedSiteID string
	SignedIn, Admin, Scoped        bool
}

// ListSiteCollections authorizes and returns the owner inventory.
func (a *Adapter) ListSiteCollections(ctx context.Context, in ListSiteCollectionsInput) ([]db.CollectionSummary, error) {
	if !in.SignedIn {
		return nil, &Error{Code: "unauthorized", Message: "unauthorized"}
	}
	in.SiteName = strings.TrimSpace(in.SiteName)
	if in.SiteName == "" {
		return nil, &Error{Code: "site_name_required", Message: "site name is required"}
	}
	siteID, err := a.OwnedSiteID(ctx, OwnedSiteInput(in))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return nil, err
	}
	return legacy.ListCollectionSummariesByID(ctx, a.database, siteID)
}

// ReplayCollectionItem retrieves the item recorded for an append retry.
func (a *Adapter) ReplayCollectionItem(ctx context.Context, siteID, coll string, id int64) (db.CollectionItem, error) {
	return legacy.GetCollectionItemByID(ctx, a.database, siteID, coll, id)
}

// SaveIdempotentResponse records the result of a successful write.
func (a *Adapter) SaveIdempotentResponse(ctx context.Context, scope []byte, status int, etag string, ref int64) error {
	return legacy.SaveIdempotentResponse(ctx, a.database, scope, status, etag, ref)
}

// ReleaseIdempotencyKey releases an unsuccessful write's reservation.
func (a *Adapter) ReleaseIdempotencyKey(ctx context.Context, scope []byte) error {
	return legacy.ReleaseIdempotencyKey(ctx, a.database, scope)
}

// ClaimWrite reserves a write or returns the previous result and its state.
func (a *Adapter) ClaimWrite(ctx context.Context, siteID, route, actorID, key string, body []byte) ([]byte, db.IdempotentResponse, string, error) {
	sum := sha256.Sum256([]byte(siteID + "\x00" + route + "\x00" + actorID + "\x00" + key))
	bodySum := sha256.Sum256(body)
	claimed, prev, err := legacy.ClaimIdempotencyKey(ctx, a.database, sum[:], siteID, bodySum[:], 2*time.Minute)
	if err != nil {
		return nil, prev, "", &Error{Code: "idempotency_internal"}
	}
	if claimed {
		return sum[:], prev, "claimed", nil
	}
	if !bytes.Equal(prev.BodyHash, bodySum[:]) {
		return nil, prev, "reused", nil
	}
	if prev.Status == 0 {
		return nil, prev, "pending", nil
	}
	return nil, prev, "replay", nil
}

// SaveEntryInput contains the values for a saved collection item.
type SaveEntryInput struct {
	SiteID     string
	Settings   db.DataSettings
	Body       []byte
	Actor      db.Actor
	MaxEntries int
}

// SaveEntry appends a declared submission or an undeclared collection item.
func (a *Adapter) SaveEntry(ctx context.Context, in SaveEntryInput) (db.CollectionItem, int64, error) {
	siteID, set, body, actor, maxEntries := in.SiteID, in.Settings, in.Body, in.Actor, in.MaxEntries
	if set.Kind == db.KindEntries {
		return a.appendEntry(ctx, siteID, set, body, actor, maxEntries)
	}
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return db.CollectionItem{}, 0, err
	}
	defer tx.Rollback()
	item, err := legacy.AppendCollectionItemTx(ctx, tx, siteID, set.Name, set.Private, body, actor)
	if err != nil {
		return item, 0, err
	}
	return item, 0, tx.Commit()
}

func (a *Adapter) appendEntry(ctx context.Context, siteID string, set db.DataSettings, body []byte, actor db.Actor, maxEntries int) (db.CollectionItem, int64, error) {
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return db.CollectionItem{}, 0, err
	}
	defer tx.Rollback()
	cur, private, err := legacy.LockEntryDeclaration(ctx, tx, siteID, set.Name)
	if err != nil {
		return db.CollectionItem{}, 0, err
	}
	if cur != db.KindEntries || private != set.Private {
		return db.CollectionItem{}, 0, db.ErrKindChanged
	}
	if set.OnePerPerson && actor.ID != "" {
		have, mine, err := legacy.LivePersonEntry(ctx, tx, siteID, set.Name, actor.ID, db.BaseEmail(actor.Email), 0)
		if err != nil {
			return db.CollectionItem{}, 0, err
		}
		if have != 0 {
			if !mine {
				have = 0
			}
			return db.CollectionItem{}, have, db.ErrOnePerPerson
		}
	}
	full, err := legacy.NameHoldsItems(ctx, tx, siteID, set.Name, maxEntries-1)
	if err != nil {
		return db.CollectionItem{}, 0, err
	}
	if full {
		return db.CollectionItem{}, 0, db.ErrNameFull
	}
	it, err := legacy.InsertEntry(ctx, tx, siteID, set.Name, body, actor)
	if err != nil {
		return db.CollectionItem{}, 0, err
	}
	return it, 0, tx.Commit()
}

// AppendQuotaError identifies a quota check failure before a collection append.
type AppendQuotaError struct {
	Full bool
}

func (e *AppendQuotaError) Error() string { return "collection append quota check failed" }

func (a *Adapter) collectionHasRoom(ctx context.Context, siteID string, growth, maxBytes int64) (bool, error) {
	if growth <= 0 || maxBytes <= 0 {
		return true, nil
	}
	used, err := legacy.SiteDataUsage(ctx, a.database, siteID)
	if err != nil {
		return false, err
	}
	return used+growth <= maxBytes, nil
}

// AppendResult contains the item and existing-entry count from an append.
type AppendResult struct {
	Item db.CollectionItem
	Have int64
}

// AppendInput contains the resolved values for a collection append.
type AppendInput struct {
	SiteID   string
	Settings db.DataSettings
	Body     []byte
	Actor    db.Actor
}

// appendCollectionItem checks quota and appends a collection item.
func (a *Adapter) appendCollectionItem(ctx context.Context, in AppendInput) (result AppendResult, err error) {
	siteID, set, body, actor := in.SiteID, in.Settings, in.Body, in.Actor
	visitor := actor.Kind == "visitor" || actor.Kind == "anonymous"
	ok, err := a.collectionHasRoom(ctx, siteID, int64(len(body)), a.deps.MaxBytes())
	if err != nil {
		return result, &AppendQuotaError{}
	}
	if !ok {
		return result, &AppendQuotaError{Full: true}
	}
	newName := false
	if visitor {
		if used, err := legacy.CollectionNameUsed(ctx, a.database, siteID, set.Name); err == nil && !used {
			newName = true
		}
	}
	result.Item, result.Have, err = a.SaveEntry(ctx, SaveEntryInput{SiteID: siteID, Settings: set, Body: body, Actor: actor, MaxEntries: a.deps.MaxEntries()})
	if err != nil {
		return result, err
	}
	if newName {
		a.Watch(ctx, siteID, a.deps.NewListMetric, 1)
	}
	a.WatchItemSize(ctx, siteID, len(body), a.deps.WatchItemKB(), a.deps.LargeItemMetric)
	return result, nil
}

// collectionVisitorEmail uses the visitor's current address for saver checks and authorship.
func (a *Adapter) collectionVisitorEmail(ctx context.Context, userID string) (string, error) {
	u, err := db.GetUserByID(ctx, a.database, userID)
	if err != nil {
		return "", err
	}
	if strings.Contains(u.Username, "@") {
		return u.Username, nil
	}
	identity, err := db.GetLatestOAuthIdentity(ctx, a.database, userID)
	if err == nil && identity.Email.Valid && identity.Email.String != "" {
		return identity.Email.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return u.Username, nil
}

// CollectionWriteActor contains request facts used by the public write gate.
type CollectionWriteActor struct {
	SiteName, Key, Mode, Cookie                        string
	Preview, ContentHost, Canonical, VisitorPass, CSRF bool
}

func (a *Adapter) collectionWriteActor(ctx context.Context, siteID, name string, in CollectionWriteActor) (db.Actor, error) {
	anon := db.Actor{Kind: "anonymous"}
	ownerID, allowAnon, err := db.GetSiteWriteGate(ctx, a.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return anon, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return anon, &Error{Code: "internal"}
	}
	suspended, reason, err := db.SiteSuspension(ctx, a.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return anon, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return anon, &Error{Code: "internal"}
	}
	if suspended {
		return anon, &Error{Code: "site_suspended", Message: reason}
	}
	offline, err := db.SiteOffline(ctx, a.database, siteID)
	if err != nil {
		return anon, &Error{Code: "internal"}
	}
	if offline && !a.deps.CollectionOwnerKeyWrite(ownerID) {
		return anon, &Error{Code: "site_offline"}
	}
	if in.Preview && in.Key == "" {
		return anon, &Error{Code: "preview_read_only"}
	}
	mode := in.Mode
	if mode == "" {
		mode = "log"
	}
	if domain, elsewhere := a.deps.CollectionDomainElsewhere(siteID); elsewhere {
		a.deps.CollectionLogAnon(siteID, in.SiteName, mode, "use_custom_domain")
		if mode == "on" && !allowAnon {
			return anon, &Error{Code: "use_custom_domain", Message: domain}
		}
		return a.deps.CollectionKeyActor(ownerID), nil
	}
	if in.ContentHost {
		if domain, exists := a.deps.CollectionSharedDomain(ctx, siteID); exists {
			a.deps.CollectionLogAnon(siteID, in.SiteName, mode, "use_custom_domain")
			if mode == "on" && !allowAnon {
				return anon, &Error{Code: "use_custom_domain", Message: domain}
			}
			return a.deps.CollectionKeyActor(ownerID), nil
		}
	}
	if in.VisitorPass {
		sess, ok := a.deps.CollectionInternalSession(siteID)
		if !ok {
			return anon, &Error{Code: "visitor_pass_forbidden"}
		}
		if !in.CSRF {
			return anon, &Error{Code: "csrf_required"}
		}
		return db.Actor{ID: sess.UserID, Kind: "visitor"}, nil
	}
	if in.Key != "" {
		u, ok, err := a.deps.CollectionResolveKey(ctx, in.Key)
		if err != nil {
			return anon, &Error{Code: "internal"}
		}
		if ok && !u.IsAdmin && u.ID != ownerID {
			log.Printf("key_write_refused user_id=%s site_id=%s name=%s route=%s collection=%s", u.ID, siteID, in.SiteName, "collection_post", name)
			return anon, &Error{Code: "site_not_found"}
		}
		if ok {
			log.Printf("key_write user_id=%s site_id=%s name=%s route=%s collection=%s", u.ID, siteID, in.SiteName, "collection_post", name)
			if u.IsAdmin && u.ID != ownerID {
				return db.Actor{ID: u.ID, Kind: "admin"}, nil
			}
			return db.Actor{ID: u.ID, Kind: "owner"}, nil
		}
		return anon, &Error{Code: "invalid_api_key"}
	}
	if mode == "off" {
		return anon, nil
	}
	if in.ContentHost && !in.Canonical {
		a.deps.CollectionLogAnon(siteID, in.SiteName, mode, "public_host")
		return anon, nil
	}
	if in.Cookie != "" && !in.ContentHost {
		if id, decErr := hex.DecodeString(in.Cookie); decErr == nil && len(id) == 32 {
			sess, sessErr := db.GetVisitorSession(ctx, a.database, id, a.deps.KeyIdleExpiry)
			if sessErr == nil && a.deps.CollectionSessionValid(sess, siteID) {
				suspended, err := db.UserSuspended(ctx, a.database, sess.UserID)
				if err != nil {
					return anon, &Error{Code: "internal"}
				}
				if suspended {
					return anon, &Error{Code: "account_suspended"}
				}
				if in.CSRF {
					_ = db.TouchVisitorSession(ctx, a.database, id, a.deps.VisitorSessionIdle)
					if sess.UserID == ownerID {
						return db.Actor{ID: sess.UserID, Kind: "owner"}, nil
					}
					return db.Actor{ID: sess.UserID, Kind: "visitor"}, nil
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
	a.deps.CollectionLogAnon(siteID, in.SiteName, mode, outcome)
	if outcome == "rejected" {
		return anon, &Error{Code: "visitor_auth_required"}
	}
	return anon, nil
}

// authorizeCollectionSaver checks the site's saver rule after the visitor gate.
func (a *Adapter) authorizeCollectionSaver(ctx context.Context, siteID string, actor db.Actor, visitorPass bool) error {
	if actor.Kind != "visitor" && actor.Kind != "anonymous" {
		return nil
	}
	email := ""
	if actor.ID != "" {
		var err error
		email, err = a.collectionVisitorEmail(ctx, actor.ID)
		if err != nil || email == "" {
			return &Error{Code: "internal"}
		}
	}
	allowed, err := db.SaverAllowed(ctx, a.database, siteID, email)
	if err != nil {
		return &Error{Code: "internal"}
	}
	if !allowed && actor.ID != "" && !visitorPass {
		ownerID, _, err := db.GetSiteWriteGate(ctx, a.database, siteID)
		allowed = err == nil && ownerID == actor.ID
	}
	if !allowed {
		return &Error{Code: "not_allowed_to_save"}
	}
	return nil
}

// CollectionBody contains a handler-decoded item and its original bytes.
type CollectionBody struct {
	Raw    []byte
	Fields map[string]json.RawMessage
}

// AppendWriteInput contains the authorized collection write and idempotency facts.
type AppendWriteInput struct {
	AppendInput
	IdempotencyKey string
	SuccessStatus  int
	DecodeBody     func() (CollectionBody, error)
	ActorFacts     CollectionWriteActor
}

// AppendWriteResult contains the saved item or the result of a replay.
type AppendWriteResult struct {
	AppendResult
	Replayed, ReplayFound bool
	ReplayStatus          int
	ReplayRef             int64
	Headers               map[string][]string
}

// appendCollectionWrite claims idempotency, appends, and records the outcome.
func (a *Adapter) appendCollectionWrite(ctx context.Context, in AppendWriteInput) (out AppendWriteResult, err error) {
	in.Actor, err = a.collectionWriteActor(ctx, in.SiteID, in.Settings.Name, in.ActorFacts)
	if err != nil {
		return out, err
	}
	if err := a.authorizeCollectionSaver(ctx, in.SiteID, in.Actor, in.ActorFacts.VisitorPass); err != nil {
		return out, err
	}
	if err := a.authorizeCollectionSubmitter(ctx, in.Settings, in.Actor); err != nil {
		return out, err
	}
	if in.Actor.Kind == "visitor" || in.Actor.Kind == "anonymous" {
		var allowed bool
		allowed, out.Headers = a.deps.CollectionAppendLimit()
		if !allowed {
			return out, &Error{Code: "rate_limited"}
		}
	}
	if in.Actor.Kind == "admin" {
		in.Actor.Email = db.OperatorLabel
	} else if in.Actor.ID != "" && in.Actor.Email == "" {
		if email, err := a.collectionVisitorEmail(ctx, in.Actor.ID); err == nil {
			in.Actor.Email = email
		}
	}
	body, err := in.DecodeBody()
	if err != nil {
		return out, err
	}
	in.Body = body.Raw
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 255 {
		return out, &Error{Code: "invalid_idempotency_key"}
	}
	var scope []byte
	saved := false
	if key != "" && in.Actor.ID != "" {
		var prev db.IdempotentResponse
		var state string
		scope, prev, state, err = a.ClaimWrite(ctx, in.SiteID, "POST collections/"+in.Settings.Name, in.Actor.ID, key, in.Body)
		if err != nil {
			return out, err
		}
		switch state {
		case "reused":
			return out, &Error{Code: "idempotency_key_reused"}
		case "pending":
			return out, &Error{Code: "idempotency_in_progress"}
		case "replay":
			out.Replayed, out.ReplayStatus, out.ReplayRef = true, prev.Status, prev.Ref
			out.Item, err = a.ReplayCollectionItem(ctx, in.SiteID, in.Settings.Name, prev.Ref)
			out.ReplayFound = err == nil
			return out, nil
		}
	}
	defer func() {
		if scope != nil && !saved {
			if err := a.ReleaseIdempotencyKey(context.WithoutCancel(ctx), scope); err != nil {
				log.Printf("idempotency release: %v", err)
			}
		}
	}()
	out.AppendResult, err = a.appendCollectionItem(ctx, in.AppendInput)
	if err != nil {
		return out, err
	}
	if scope != nil {
		saved = true
		if err := a.SaveIdempotentResponse(context.WithoutCancel(ctx), scope, in.SuccessStatus, "", out.Item.ID); err != nil {
			log.Printf("idempotency save: %v", err)
		}
	}
	return out, nil
}

// appendBoard writes a board item after checking its owner or visitor.
func (a *Adapter) appendBoard(ctx context.Context, in AppendWriteInput) (out AppendWriteResult, err error) {
	siteID, set, facts := in.SiteID, in.Settings, in.ActorFacts
	suspended, reason, err := db.SiteSuspension(ctx, a.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return out, &Error{Code: "internal"}
	}
	if suspended {
		return out, &Error{Code: "site_suspended", Message: reason}
	}
	var actor db.Actor
	if facts.Key != "" || a.deps.CollectionOwnerBrowser(siteID) {
		ownerID, _, err := db.GetSiteWriteGate(ctx, a.database, siteID)
		if err != nil {
			return out, &Error{Code: "site_not_found"}
		}
		if facts.Key != "" {
			u, ok, err := a.deps.CollectionResolveKey(ctx, facts.Key)
			if err != nil {
				return out, &Error{Code: "internal"}
			}
			if !ok {
				return out, &Error{Code: "invalid_api_key"}
			}
			if !u.IsAdmin && u.ID != ownerID {
				return out, &Error{Code: "site_not_found"}
			}
			actor = db.Actor{ID: u.ID, Kind: "owner"}
			if u.ID != ownerID {
				actor.Kind = "admin"
			}
		} else if a.deps.CollectionOwnerBrowser(siteID) {
			if !facts.CSRF {
				return out, &Error{Code: "csrf_required"}
			}
			actor = db.Actor{ID: ownerID, Kind: "owner"}
		} else {
			return out, &Error{Code: "owner_only"}
		}
	} else {
		offline, err := db.SiteOffline(ctx, a.database, siteID)
		if err != nil {
			return out, &Error{Code: "internal"}
		}
		if offline {
			return out, &Error{Code: "site_offline"}
		}
		if facts.Preview {
			return out, &Error{Code: "preview_read_only"}
		}
		if facts.ContentHost {
			return out, &Error{Code: "visitor_auth_required"}
		}
		sess := a.deps.CollectionStrictSession(siteID)
		if !sess.OK {
			return out, &Error{Code: "visitor_auth_required"}
		}
		suspended, err := db.UserSuspended(ctx, a.database, sess.UserID)
		if err != nil {
			return out, &Error{Code: "internal"}
		}
		if suspended {
			return out, &Error{Code: "account_suspended"}
		}
		if !facts.CSRF {
			return out, &Error{Code: "csrf_required"}
		}
		_ = db.TouchVisitorSession(ctx, a.database, sess.ID, a.deps.VisitorSessionIdle)
		actor = db.Actor{ID: sess.UserID, Kind: "visitor"}
		if err := a.authorizeCollectionSaver(ctx, siteID, actor, facts.VisitorPass); err != nil {
			return out, err
		}
		var allowed bool
		allowed, out.Headers = a.deps.CollectionAppendLimit()
		if !allowed {
			return out, &Error{Code: "rate_limited"}
		}
		if !a.deps.CollectionBoardLimit(sess.UserID) {
			return out, &Error{Code: "rate_limited"}
		}
	}
	if actor.Kind == "admin" {
		actor.Email = db.OperatorLabel
	} else if actor.ID != "" && actor.Email == "" {
		if email, err := a.collectionVisitorEmail(ctx, actor.ID); err == nil {
			actor.Email = email
		}
	}
	decoded, err := in.DecodeBody()
	if err != nil {
		return out, err
	}
	raw, obj := decoded.Raw, decoded.Fields
	body := json.RawMessage(bytes.TrimSpace(raw))
	stamped := false
	for _, key := range []string{"_submitted_by", "_submitted_at"} {
		if _, ok := obj[key]; ok {
			delete(obj, key)
			stamped = true
		}
	}
	if stamped {
		body, err = json.Marshal(obj)
		if err != nil {
			return out, &Error{Code: "internal"}
		}
	}
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 255 {
		return out, &Error{Code: "invalid_idempotency_key"}
	}
	var scope []byte
	saved := false
	if key != "" && actor.ID != "" {
		var prev db.IdempotentResponse
		var state string
		scope, prev, state, err = a.ClaimWrite(ctx, siteID, "POST collections/"+set.Name, actor.ID, key, body)
		if err != nil {
			return out, err
		}
		switch state {
		case "reused":
			return out, &Error{Code: "idempotency_key_reused"}
		case "pending":
			return out, &Error{Code: "idempotency_in_progress"}
		case "replay":
			out.Replayed, out.ReplayStatus, out.ReplayRef = true, prev.Status, prev.Ref
			out.Item, err = a.ReplayCollectionItem(ctx, siteID, set.Name, prev.Ref)
			out.ReplayFound = err == nil
			return out, nil
		}
	}
	defer func() {
		if scope != nil && !saved {
			if err := a.ReleaseIdempotencyKey(context.WithoutCancel(ctx), scope); err != nil {
				log.Printf("idempotency release: %v", err)
			}
		}
	}()
	room, err := a.collectionHasRoom(ctx, siteID, int64(len(body)), a.deps.MaxBytes())
	if err != nil {
		return out, &AppendQuotaError{}
	}
	if !room {
		return out, &AppendQuotaError{Full: true}
	}
	out.Item, _, err = db.AppendEntry(ctx, a.database, siteID, set.Name, db.KindBoard, set.Private, body, actor, false, a.deps.BoardMax())
	if err != nil {
		return out, err
	}
	out.Item.Version = 1
	a.WatchItemSize(ctx, siteID, len(body), a.deps.WatchItemKB(), a.deps.LargeItemMetric)
	if scope != nil {
		saved = true
		if err := a.SaveIdempotentResponse(context.WithoutCancel(ctx), scope, in.SuccessStatus, "", out.Item.ID); err != nil {
			log.Printf("idempotency save: %v", err)
		}
	}
	out.Item.Data = WithoutSubmitter(out.Item.Data)
	out.Item.By = ""
	return out, nil
}

// CollectionPrivateHome describes a site's own address.
type CollectionPrivateHome struct {
	Host, OwnerID string
	IsDomain      bool
}

// appendPrivate writes a private list item from a signed-in visitor.
func (a *Adapter) appendPrivate(ctx context.Context, in AppendWriteInput) (out AppendWriteResult, err error) {
	siteID, set, facts := in.SiteID, in.Settings, in.ActorFacts
	suspended, reason, err := db.SiteSuspension(ctx, a.database, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, &Error{Code: "site_not_found"}
	}
	if err != nil {
		return out, &Error{Code: "internal"}
	}
	if suspended {
		return out, &Error{Code: "site_suspended", Message: reason}
	}
	offline, err := db.SiteOffline(ctx, a.database, siteID)
	if err != nil {
		return out, &Error{Code: "internal"}
	}
	if offline {
		return out, &Error{Code: "site_offline"}
	}
	if facts.Preview {
		return out, &Error{Code: "preview_read_only"}
	}
	info, here, err := a.deps.CollectionPrivateHome(siteID)
	if err != nil {
		return out, &Error{Code: "internal"}
	}
	if info.IsDomain && !here && a.deps.CollectionPrivateWrongHost() {
		return out, &Error{Code: "use_custom_domain", Message: info.Host}
	}
	if facts.Key != "" {
		return out, &Error{Code: "private_visitor_only"}
	}
	if !here {
		return out, &Error{Code: "private_needs_own_domain"}
	}
	sess := a.deps.CollectionStrictSession(siteID)
	if !sess.OK {
		a.deps.CollectionLogAnon(siteID, facts.SiteName, "on", "private_rejected")
		return out, &Error{Code: "visitor_auth_required"}
	}
	if !facts.CSRF {
		return out, &Error{Code: "csrf_required"}
	}
	visitor := sess.UserID != info.OwnerID || facts.VisitorPass
	if visitor {
		var allowed bool
		allowed, out.Headers = a.deps.CollectionAppendLimit()
		if !allowed {
			return out, &Error{Code: "rate_limited"}
		}
	}
	email, err := a.collectionVisitorEmail(ctx, sess.UserID)
	if err != nil || email == "" {
		return out, &Error{Code: "internal"}
	}
	if visitor {
		allowed, err := db.SaverAllowed(ctx, a.database, siteID, email)
		if err != nil {
			return out, &Error{Code: "internal"}
		}
		if !allowed {
			return out, &Error{Code: "not_allowed_to_save"}
		}
	}
	decoded, err := in.DecodeBody()
	if err != nil {
		return out, err
	}
	raw, fields := decoded.Raw, decoded.Fields
	key := strings.TrimSpace(in.IdempotencyKey)
	if len(key) > 255 {
		return out, &Error{Code: "invalid_idempotency_key"}
	}
	var scope []byte
	saved := false
	if key != "" && sess.UserID != "" {
		var prev db.IdempotentResponse
		var state string
		scope, prev, state, err = a.ClaimWrite(ctx, siteID, "POST collections/"+set.Name, sess.UserID, key, raw)
		if err != nil {
			return out, err
		}
		switch state {
		case "reused":
			return out, &Error{Code: "idempotency_key_reused"}
		case "pending":
			return out, &Error{Code: "idempotency_in_progress"}
		case "replay":
			out.Replayed, out.ReplayStatus, out.ReplayRef = true, prev.Status, prev.Ref
			out.Item, err = a.ReplayCollectionItem(ctx, siteID, set.Name, prev.Ref)
			out.ReplayFound = err == nil
			return out, nil
		}
	}
	defer func() {
		if scope != nil && !saved {
			if err := a.ReleaseIdempotencyKey(context.WithoutCancel(ctx), scope); err != nil {
				log.Printf("idempotency release: %v", err)
			}
		}
	}()
	by, _ := json.Marshal(email)
	at, _ := json.Marshal(time.Now().UTC().Format(time.RFC3339))
	fields["_submitted_by"], fields["_submitted_at"] = by, at
	body, err := json.Marshal(fields)
	if err != nil {
		return out, &Error{Code: "invalid_item"}
	}
	room, err := a.collectionHasRoom(ctx, siteID, int64(len(body)), a.deps.MaxBytes())
	if err != nil {
		return out, &AppendQuotaError{}
	}
	if !room {
		return out, &AppendQuotaError{Full: true}
	}
	out.Item, out.Have, err = a.SaveEntry(ctx, SaveEntryInput{SiteID: siteID, Settings: set, Body: body, Actor: db.Actor{ID: sess.UserID, Email: email, Kind: "visitor"}, MaxEntries: a.deps.MaxEntries()})
	if err != nil {
		return out, err
	}
	_ = db.TouchVisitorSession(ctx, a.database, sess.ID, a.deps.VisitorSessionIdle)
	a.WatchItemSize(ctx, siteID, len(body), a.deps.WatchItemKB(), a.deps.LargeItemMetric)
	if scope != nil {
		saved = true
		if err := a.SaveIdempotentResponse(context.WithoutCancel(ctx), scope, in.SuccessStatus, "", out.Item.ID); err != nil {
			log.Printf("idempotency save: %v", err)
		}
	}
	return out, nil
}

// AppendCollectionInput contains request facts for a collection append.
type AppendCollectionInput struct {
	Gate           CollectionGateInput
	ActorFacts     CollectionWriteActor
	IdempotencyKey string
	SuccessStatus  int
	DecodeBody     func(string, db.DataSettings) (CollectionBody, error)
}

// AppendCollectionResult reports the reached response state and saved item.
type AppendCollectionResult struct {
	AppendWriteResult
	Settings     db.DataSettings
	Branch       string
	PrivateCache bool
}

// AppendCollection resolves, authorizes and appends one collection item.
func (a *Adapter) AppendCollection(ctx context.Context, in AppendCollectionInput) (out AppendCollectionResult, err error) {
	headers := map[string][]string{}
	if in.Gate.SiteName != "" && ValidCollectionName(in.Gate.Collection) && !in.Gate.KeyWithoutPage {
		var originHeaders map[string][]string
		in.Gate.OriginAllowed, originHeaders = a.deps.CollectionOrigin(in.Gate.SiteName)
		for k, v := range originHeaders {
			headers[k] = v
		}
	}
	siteID, set, err := a.prepareCollectionAppend(ctx, in.Gate)
	out.Settings = set
	if err == nil {
		out.Branch = "public"
		if set.Kind == db.KindBoard {
			out.Branch = "board"
			out.PrivateCache = true
		} else if set.Private {
			out.Branch = "private"
			out.PrivateCache = true
		}
		write := AppendWriteInput{AppendInput: AppendInput{SiteID: siteID, Settings: set}, ActorFacts: in.ActorFacts, IdempotencyKey: in.IdempotencyKey, SuccessStatus: in.SuccessStatus, DecodeBody: func() (CollectionBody, error) { return in.DecodeBody(out.Branch, set) }}
		switch out.Branch {
		case "board":
			out.AppendWriteResult, err = a.appendBoard(ctx, write)
		case "private":
			out.AppendWriteResult, err = a.appendPrivate(ctx, write)
		default:
			out.AppendWriteResult, err = a.appendCollectionWrite(ctx, write)
		}
	}
	for k, v := range out.Headers {
		headers[k] = v
	}
	out.Headers = headers
	return out, err
}

// IsCollectionPrivate reads the collection's privacy setting.
func (a *Adapter) IsCollectionPrivate(ctx context.Context, siteID, coll string) (bool, error) {
	return legacy.IsCollectionPrivate(ctx, a.database, siteID, coll)
}

// CollectionExportKeys checks existence before reading the export columns.
func (a *Adapter) CollectionExportKeys(ctx context.Context, siteID, coll string) ([]string, bool, error) {
	exists, err := legacy.CollectionExistsByID(ctx, a.database, siteID, coll)
	if err != nil || !exists {
		return nil, exists, err
	}
	keys, err := legacy.ListCollectionKeysByID(ctx, a.database, siteID, coll)
	return keys, true, err
}

// CollectionCSVInput identifies the collection and columns to export.
type CollectionCSVInput struct {
	SiteID, Collection string
	Keys               []string
}

// CollectionCSVRows yields export row values, closing the query when iteration stops.
func (a *Adapter) CollectionCSVRows(ctx context.Context, in CollectionCSVInput) iter.Seq2[[]string, error] {
	return func(yield func([]string, error) bool) {
		stopped := errors.New("export iteration stopped")
		err := legacy.ForEachCollectionItemByID(ctx, a.database, in.SiteID, in.Collection, func(it db.CollectionItem) error {
			if !yield(CollectionCSVRow(it, in.Keys, a.deps.CSVSafe), nil) {
				return stopped
			}
			return nil
		})
		if err != nil && err != stopped {
			yield(nil, err)
		}
	}
}

// CollectionCSVRow shapes one item for an owner CSV export.
func CollectionCSVRow(it db.CollectionItem, keys []string, safe func(string) string) []string {
	row := make([]string, 3+len(keys))
	row[0] = strconv.FormatInt(it.ID, 10)
	row[1] = it.CreatedAt.UTC().Format(time.RFC3339)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(it.Data, &fields); err != nil {
		fields = map[string]json.RawMessage{}
	}
	for i, k := range keys {
		row[2+i] = JSONCSVCell(fields[k], safe)
	}
	row[2+len(keys)] = safe(it.By)
	return row
}

// JSONCSVCell renders a JSON value for a CSV cell.
func JSONCSVCell(raw json.RawMessage, safe func(string) string) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return safe(s)
		}
	case '{', '[':
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			b, err := json.Marshal(v)
			if err == nil {
				return string(b)
			}
		}
	}
	return string(raw)
}

// CollectionCSVHeader shapes the header of an owner CSV export.
func CollectionCSVHeader(keys []string, safe func(string) string) []string {
	header := make([]string, 0, 3+len(keys))
	header = append(header, "id", "created_at")
	for _, k := range keys {
		header = append(header, safe(k))
	}
	// Last: who sent each item (the owner's download only).
	return append(header, "sent_by")
}

// UpdateCollectionItem updates a declared item and records its previous value.
func (a *Adapter) UpdateCollectionItem(ctx context.Context, siteID, collection string, id int64, actor db.Actor, maxBytes int64, fn func(json.RawMessage) (json.RawMessage, error)) (db.CollectionItem, error) {
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		return db.CollectionItem{}, err
	}
	defer tx.Rollback()
	cur, err := legacy.LockCollectionItem(ctx, tx, siteID, collection, id)
	if err != nil {
		return db.CollectionItem{}, err
	}
	old := cur.Data
	next, err := fn(old)
	if err != nil {
		return db.CollectionItem{}, err
	}
	it, err := legacy.UpdateCollectionItem(ctx, tx, siteID, collection, id, next)
	if err != nil {
		return db.CollectionItem{}, err
	}
	if err := legacy.RecordHistory(ctx, tx, siteID, db.HistoryList, collection, &id, db.OpEdit, old, actor); err != nil {
		return db.CollectionItem{}, err
	}
	if len(it.Data) > len(old) && maxBytes > 0 {
		used, err := legacy.SiteDataUsage(ctx, tx, siteID)
		if err != nil {
			return db.CollectionItem{}, err
		}
		if used > maxBytes {
			return db.CollectionItem{}, db.ErrSiteFull
		}
	}
	return it, tx.Commit()
}
