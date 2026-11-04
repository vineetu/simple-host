package legacy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
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
	originAllowed := in.OriginAllowed
	if !in.KeyWithoutPage && a.deps.AuthorizeCollectionOrigin != nil {
		originAllowed = a.deps.AuthorizeCollectionOrigin(in.SiteName)
	}
	if !in.KeyWithoutPage && !originAllowed {
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

// AppendCollection runs the full append sequence, including transport callbacks at
// the points where request handling and response writing must occur.
func (a *Adapter) AppendCollection(ctx context.Context, in CollectionGateInput) (db.DataSettings, error) {
	siteID, set, err := a.prepareCollectionAppend(ctx, in)
	if err != nil {
		return set, err
	}
	// Boards and private lists use their own write paths before the public gate.
	if set.Kind == db.KindBoard {
		a.deps.CollectionAppendBoard(siteID, set)
		return set, nil
	}
	if set.Private {
		a.deps.CollectionAppendPrivate(siteID, set)
		return set, nil
	}
	actor, ok := a.deps.CollectionAppendActor(siteID, set)
	if !ok {
		return set, nil
	}
	// Declared submissions require a signed-in visitor before rate limiting
	// or reading the request body.
	if err := a.authorizeCollectionSubmitter(ctx, set, actor); err != nil {
		return set, err
	}
	input, ok := a.deps.CollectionAppendPrepare(siteID, set, actor)
	if !ok {
		return set, nil
	}
	if err := a.validateCollectionBody(input.Body); err != nil {
		return set, err
	}
	a.appendCollectionOnce(ctx, input)
	return set, nil
}

func (a *Adapter) validateCollectionBody(body []byte) error {
	if len(body) == 0 || !json.Valid(body) {
		return &Error{Code: "invalid_collection_body", Message: "body must be a JSON value"}
	}
	if !a.deps.CollectionJSONStorable(body) {
		return &Error{Code: "invalid_json", Message: `the JSON holds text that cannot be saved: a NUL character (\u0000), half of a surrogate pair (\ud800 alone) or bytes that are not UTF-8. Remove it and send again`}
	}
	return nil
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

// CollectionPrivateReadAllowed decides whether a private list can be read
// before checking the browser's owner session.
func CollectionPrivateReadAllowed(ownerKey, sharedHost, originless bool) bool {
	if ownerKey {
		return !sharedHost
	}
	return !originless
}

// listCollectionSettings reads the declaration and excludes personal records.
func (a *Adapter) listCollectionSettings(ctx context.Context, siteID, coll string) (db.DataSettings, error) {
	set, err := a.collectionSettings(ctx, siteID, coll)
	if err != nil {
		return set, err
	}
	if set.Kind == db.KindPersonal {
		return set, ErrPersonalList
	}
	return set, nil
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

// CollectionPageRequest contains the request facts for a complete list read.
type CollectionPageRequest struct {
	CollectionListSiteInput
	Limit, Before string
	SharedHost    bool
}

// CollectionPageResult is the page and declaration selected for a list read.
type CollectionPageResult struct {
	Items        []db.CollectionItem
	Next         *int64
	Settings     db.DataSettings
	PrivateCache bool
	AuthorView   bool
}

// ReadCollection performs site selection, read checks and the page query in order.
func (a *Adapter) ReadCollection(ctx context.Context, in CollectionPageRequest) (CollectionPageResult, error) {
	var out CollectionPageResult
	in.SiteName = strings.TrimSpace(in.SiteName)
	in.Collection = strings.TrimSpace(in.Collection)
	siteID, owner, err := a.collectionListSite(ctx, in.CollectionListSiteInput)
	if err != nil {
		return out, err
	}
	if !a.deps.CollectionReadAllowed(siteID, owner) {
		return out, &Error{Code: "read_limited"}
	}
	if !owner {
		suspended, reason, err := db.SiteSuspension(ctx, a.database, siteID)
		if err != nil {
			return out, err
		}
		if suspended {
			return out, &Error{Code: "site_suspended", Message: reason}
		}
		offline, err := db.SiteOffline(ctx, a.database, siteID)
		if err != nil {
			return out, &Error{Code: "internal", Message: err.Error()}
		}
		if offline {
			return out, &Error{Code: "site_offline"}
		}
	}
	out.Settings, err = a.listCollectionSettings(ctx, siteID, in.Collection)
	if errors.Is(err, ErrPersonalList) {
		return out, err
	}
	if err != nil {
		return out, &Error{Code: "internal", Message: err.Error()}
	}
	if out.Settings.Private {
		out.PrivateCache = true
		if !CollectionPrivateReadAllowed(owner, in.SharedHost, in.Originless) {
			return out, &Error{Code: "private_not_found"}
		}
		if err := a.deps.CollectionPrivateRead(siteID, owner); err != nil {
			return out, err
		}
	}
	withAuthor := a.deps.CollectionAuthorView(siteID, owner)
	out.AuthorView = withAuthor
	out.Items, out.Next, err = a.listCollection(ctx, CollectionListInput{SiteID: siteID, Collection: in.Collection, Limit: in.Limit, Before: in.Before, WithAuthor: withAuthor, Settings: out.Settings})
	if err != nil {
		return out, &Error{Code: "internal", Message: err.Error()}
	}
	return out, nil
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
	Collection string
	Originless bool
}

// CollectionListSite selects the owner-key site or applies the public origin gate.
func (a *Adapter) collectionListSite(ctx context.Context, in CollectionListSiteInput) (string, bool, error) {
	if err := validateCollectionTarget(in.SiteName, in.Collection); err != nil {
		return "", false, err
	}
	id, owner := a.OwnerSiteFromKey(ctx, in.OwnerKeyInput)
	if !owner {
		if !in.Originless {
			if err := a.AuthorizeCollectionGate(ctx, CollectionGateInput{SiteName: in.SiteName, Collection: in.Collection}); err != nil {
				return "", false, err
			}
		}
		var err error
		id, err = a.deps.ResolveNamedSite(ctx, in.SiteName)
		if err != nil {
			return "", false, err
		}
	}
	if in.Scoped && in.ScopedSiteID != id {
		return "", false, sql.ErrNoRows
	}
	return id, owner, nil
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
		return nil, prev, "", err
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

// ClaimError identifies a failed idempotency reservation.
type ClaimError struct{ Err error }

func (e *ClaimError) Error() string { return e.Err.Error() }

// AppendResult contains an append or an idempotent replay.
type AppendResult struct {
	Item        db.CollectionItem
	Have        int64
	State       string
	Previous    db.IdempotentResponse
	ReplayFound bool
}

// AppendInput contains the resolved values for a collection append.
type AppendInput struct {
	SiteID, Key   string
	Settings      db.DataSettings
	Body          []byte
	Actor         db.Actor
	SuccessStatus int
}

// appendCollectionOnce reserves a retry key and runs the collection append.
func (a *Adapter) appendCollectionOnce(ctx context.Context, in AppendInput) (result AppendResult, err error) {
	siteID, set, body, actor, key := in.SiteID, in.Settings, in.Body, in.Actor, in.Key
	var scope []byte
	saved := false
	completed := false
	defer func() {
		if completed && scope != nil && !saved {
			if releaseErr := legacy.ReleaseIdempotencyKey(context.WithoutCancel(ctx), a.database, scope); releaseErr != nil {
				log.Printf("idempotency release: %v", releaseErr)
			}
		}
	}()
	result, err = func() (result AppendResult, err error) {
		if len(key) > 255 {
			return result, &Error{Code: "invalid_idempotency_key", Message: "Idempotency-Key is at most 255 characters"}
		}
		if key != "" && actor.ID != "" {
			var state string
			scope, result.Previous, state, err = a.ClaimWrite(ctx, siteID, "POST collections/"+set.Name, actor.ID, key, body)
			if err != nil {
				return result, &ClaimError{Err: err}
			}
			if state != "claimed" {
				result.State = state
				if state == "replay" {
					result.Item, err = a.ReplayCollectionItem(ctx, siteID, set.Name, result.Previous.Ref)
					result.ReplayFound = err == nil
					err = nil
				}
				return result, nil
			}
		}
		visitor := actor.Kind == "visitor" || actor.Kind == "anonymous"
		maxEntries, watchItemKB, maxBytes := a.deps.MaxEntries, a.deps.WatchItemKB, a.deps.MaxBytes
		newListMetric, largeItemMetric, successStatus := a.deps.NewListMetric, a.deps.LargeItemMetric, in.SuccessStatus
		ok, err := a.collectionHasRoom(ctx, siteID, int64(len(body)), maxBytes)
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
		item, have, err := a.SaveEntry(ctx, SaveEntryInput{SiteID: siteID, Settings: set, Body: body, Actor: actor, MaxEntries: maxEntries})
		if err != nil {
			result.Item, result.Have = item, have
			return result, err
		}
		if newName {
			a.Watch(ctx, siteID, newListMetric, 1)
		}
		a.WatchItemSize(ctx, siteID, len(body), watchItemKB, largeItemMetric)
		if scope != nil {
			// Keep it even if the caller has gone: that is the retry case.
			if err := legacy.SaveIdempotentResponse(context.WithoutCancel(ctx), a.database, scope, successStatus, "", item.ID); err != nil {
				log.Printf("idempotency save: %v", err)
			}
		}
		result.Item, result.Have = item, have
		saved = true
		return result, nil
	}()
	completed = true
	a.deps.CollectionAppendRespond(set, result, err)
	return result, err
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

// forEachCollectionItem walks the live items of an owner export.
func (a *Adapter) forEachCollectionItem(ctx context.Context, siteID, coll string, fn func(db.CollectionItem) error) error {
	return legacy.ForEachCollectionItemByID(ctx, a.database, siteID, coll, fn)
}

// ForEachCollectionCSVRow reads and shapes each row of a collection export.
func (a *Adapter) ForEachCollectionCSVRow(ctx context.Context, siteID, coll string, keys []string, safe func(string) string, fn func([]string) error) error {
	return a.forEachCollectionItem(ctx, siteID, coll, func(it db.CollectionItem) error {
		return fn(CollectionCSVRow(it, keys, safe))
	})
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
