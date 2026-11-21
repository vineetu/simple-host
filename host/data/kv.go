package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"

	"golang.org/x/text/unicode/norm"
)

// NormalizeKVKey normalizes a key before a KV write.
func NormalizeKVKey(key string) string { return norm.NFC.String(key) }

// StampID identifies the author of a new entry.
func StampID(owner bool, visitorID string) string {
	if owner {
		return ""
	}
	return visitorID
}

// CheckPostLimits keeps a post from someone not signed in small and mostly
// free of links. value is the JSON the page sent.
func CheckPostLimits(value json.RawMessage, maxBytes, maxLinks int) error {
	if len(value) > maxBytes {
		return &Error{Code: "post_too_large", Message: fmt.Sprintf("a post here is at most %d bytes", maxBytes)}
	}
	var v any
	if json.Unmarshal(value, &v) != nil {
		return &Error{Code: "invalid_value", Message: "valid JSON required"}
	}
	var b strings.Builder
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			b.WriteString(strings.ToLower(t))
			b.WriteByte(' ')
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for k, e := range t {
				walk(k)
				walk(e)
			}
		}
	}
	walk(v)
	text := b.String()
	if strings.Count(text, "http://")+strings.Count(text, "https://")+strings.Count(text, "www.") > maxLinks {
		return &Error{Code: "too_many_links", Message: fmt.Sprintf("a post here may hold at most %d links", maxLinks)}
	}
	return nil
}

// prepareKVWrite resolves the existing author and the action for a write.
func (s *Service) prepareKVWrite(conn *sqlite3.Conn, key, requested string) (string, bool, string, error) {
	writer, err := kvWriter(conn, key)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, "", err
	}
	if requested == "add" && exists {
		return "", false, "", &Error{Code: "key_exists", Message: "this key already exists"}
	}
	if requested == "edit" && !exists {
		return "", false, "", &Error{Code: "key_not_found", Message: "key not found"}
	}
	action := "add"
	if exists {
		action = "edit"
	}
	return writer, exists, action, nil
}

// kvWriteNotification describes the notification for a successful write.
func kvWriteNotification(key string, value json.RawMessage, exists bool) (string, map[string]any) {
	name := "record.added"
	data := map[string]any{"key": key, "fields": value}
	if exists {
		name = "record.changed"
		data["change"] = "edited"
	}
	return name, data
}

// kvDeleteNotification describes the notification for a successful deletion.
func kvDeleteNotification(key string, deleted int64) (string, map[string]any) {
	if deleted == 0 {
		return "", nil
	}
	return "record.changed", map[string]any{"key": key, "change": "deleted"}
}

// finishKVWrite releases the write lock before selecting the notification.
func finishKVWrite(unlock func(), key string, value json.RawMessage, exists bool) (string, map[string]any) {
	unlock()
	return kvWriteNotification(key, value, exists)
}

// kvAuthorVisibility resolves author information for the caller.
func kvAuthorVisibility(writer, visitorID string, owner bool) (string, *bool) {
	if owner {
		return writer, nil
	}
	mine := writer != "" && writer == visitorID
	return "", &mine
}

// VisibleKVItem is a list entry with author visibility resolved.
type VisibleKVItem struct {
	Key       string
	Value     json.RawMessage
	VisitorID string
	Mine      *bool
}

// ListVisibleKV reads a page and resolves its author visibility.
func (s *Service) ListVisibleKV(ctx context.Context, call Call, prefix, after string, limit int) ([]VisibleKVItem, string, error) {
	var rows []runtime.KVEntry
	var next string
	err := s.withKV(ctx, call, false, func(ctx context.Context, conn *sqlite3.Conn, _ *Admission) error {
		var err error
		var items []runtime.KVEntry
		if call.Owner {
			items, err = runtime.RuntimeKVList(conn, prefix, after, limit+1, "")
		} else {
			items, err = s.listRuleKV(ctx, conn, &call, prefix, after, limit+1)
		}
		if err != nil {
			return err
		}
		if len(items) > limit {
			next = items[limit-1].Key
			items = items[:limit]
		}
		rows = items
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	out := make([]VisibleKVItem, 0, len(rows))
	for _, row := range rows {
		writer, mine := kvAuthorVisibility(row.VisitorID, call.VisitorID, call.Owner || call.OwnerOnSite)
		out = append(out, VisibleKVItem{Key: row.Key, Value: row.Value, VisitorID: writer, Mine: mine})
	}
	return out, next, nil
}

// GetVisibleKV reads a key and resolves its author visibility.
func (s *Service) GetVisibleKV(ctx context.Context, call Call, key string) (VisibleKVItem, error) {
	var item runtime.KVEntry
	err := s.withKV(ctx, call, false, func(ctx context.Context, conn *sqlite3.Conn, _ *Admission) error {
		var err error
		if call.Owner {
			item, err = runtime.RuntimeKVGet(conn, key, "")
		} else {
			var items []runtime.KVEntry
			items, err = s.getRuleKV(ctx, conn, &call, key)
			if err == nil && len(items) == 0 {
				err = sql.ErrNoRows
			}
			if len(items) > 0 {
				item = items[0]
			}
		}
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "key_not_found", Message: "key not found"}
		}
		return err
	})
	if err != nil {
		return VisibleKVItem{}, err
	}
	writer, mine := kvAuthorVisibility(item.VisitorID, call.VisitorID, call.Owner || call.OwnerOnSite)
	return VisibleKVItem{Key: item.Key, Value: item.Value, VisitorID: writer, Mine: mine}, nil
}

// KVReadInput identifies a key and the caller requesting it.
type KVReadInput struct {
	Call Call
	Key  string
}

// KVReadResult carries the entry and any ordered authorization responses.
type KVReadResult struct {
	Item          VisibleKVItem
	Authorization *Error
}

// GetKV authorizes and reads a single key.
func (s *Service) GetKV(ctx context.Context, in KVReadInput) (KVReadResult, error) {
	result := KVReadResult{}
	if result.Authorization = s.authorize(ctx, &in.Call, "kv", ruleengine.OpRead); result.Authorization != nil {
		return result, nil
	}
	var err error
	result.Item, err = s.GetVisibleKV(ctx, in.Call, in.Key)
	return result, err
}

// KVListInput identifies a page and the caller requesting it.
type KVListInput struct {
	Call          Call
	Prefix, After string
	Limit         int
	ValidLimit    bool
}

// KVListResult carries the page and ordered authorization responses.
type KVListResult struct {
	Items         []VisibleKVItem
	Next          string
	Authorization *Error
}

// ListKV authorizes and reads a page of keys.
func (s *Service) ListKV(ctx context.Context, in KVListInput) (KVListResult, error) {
	result := KVListResult{}
	if result.Authorization = s.authorize(ctx, &in.Call, "kv", ruleengine.OpRead); result.Authorization != nil {
		return result, nil
	}
	if !in.ValidLimit {
		return result, &Error{Code: "invalid_limit", Message: "invalid limit"}
	}
	var err error
	result.Items, result.Next, err = s.ListVisibleKV(ctx, in.Call, in.Prefix, in.After, in.Limit)
	return result, err
}

// KVPutInput identifies a value and the caller requesting a write.
type KVPutInput struct {
	Call           Call
	Key, Requested string
	Body           io.Reader
}

// PutKV resolves, authorizes and writes a key under the site lock.
func (s *Service) PutKV(ctx context.Context, in KVPutInput) (json.RawMessage, *Error, error) {
	call := in.Call
	if err := s.Gate(call, true); err != nil {
		return nil, err.(*Error), nil
	}
	value, err := decodeKVValue(in.Body)
	if err != nil {
		return nil, nil, err
	}
	requested := in.Requested
	if !call.VisitorAI {
		requested = ""
	}
	ctx, unlock, err := s.writeLock(ctx, call, true)
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	var writer string
	var exists bool
	err = s.withKV(ctx, call, true, func(ctx context.Context, conn *sqlite3.Conn, admission *Admission) error {
		var err error
		writer, exists, err = s.writeKVSQLite(ctx, conn, admission, &call, in.Key, value, requested)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	name, data := finishKVWrite(unlock, in.Key, value, exists)
	// writer is the key's own author (from prepareKVWrite, already read):
	// the actor's own id for a visitor's own write, or -- for an owner's
	// write to an existing key -- whoever wrote it first. Web-push
	// fan-out keys off this the same way row_write.go's row_visitor_id
	// does; call.Owner (specifically, not OwnerOnSite -- D1's own write
	// stays an ordinary notifying write, as it always has been) is the
	// actor. A brand-new key an owner creates with no prior writer stays
	// untargeted, same as a plain owner row insert.
	data["owner_write"] = call.Owner
	if writer != "" {
		data["row_visitor_id"] = writer
	}
	s.Emit(Event{Call: call, Name: name, Data: data})
	return value, nil, nil
}

// decodeKVValue reads a write's body, which must hold a JSON value.
func decodeKVValue(body io.Reader) (json.RawMessage, error) {
	raw, err := io.ReadAll(body)
	if err != nil {
		return nil, &Error{Code: "invalid_value", Message: "value too large"}
	}
	var v struct {
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &v) != nil || len(v.Value) == 0 || !json.Valid(v.Value) {
		return nil, &Error{Code: "invalid_value", Message: "valid JSON value required"}
	}
	return v.Value, nil
}

// writeKVSQLite stores value under key in the open namespace and returns the
// key's author and whether it existed. The owner's tools are never limited by
// rules (authorize); a visitor's write is decided by the rules inside one
// immediate transaction.
func (s *Service) writeKVSQLite(ctx context.Context, conn *sqlite3.Conn, admission *Admission, call *Call, key string, value json.RawMessage, requested string) (string, bool, error) {
	if call.Owner {
		writer, exists, _, err := s.prepareKVWrite(conn, key, requested)
		if err != nil {
			return writer, exists, err
		}
		return writer, exists, s.putKVSQLite(ctx, conn, admission, *call, key, value, exists)
	}
	if err := conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return "", false, err
	}
	defer conn.Exec("ROLLBACK")
	// Resolve existence and the AI action before active-account checks.
	writer, exists, action, err := s.prepareKVWrite(conn, key, requested)
	if err != nil {
		return writer, exists, err
	}
	call.Context = ctx
	if err := s.prepareKVRuleWrite(call, action, exists); err != nil {
		return writer, exists, err
	}
	if call.CheckedPost {
		if err := CheckPostLimits(value, s.deps.SpamCheckedMaxBytes, s.deps.SpamCheckedMaxLinks); err != nil {
			return writer, exists, err
		}
	}
	return writer, exists, s.putKVSQLite(ctx, conn, admission, *call, key, value, exists)
}

// KVDeleteInput identifies a key and the caller requesting deletion.
type KVDeleteInput struct {
	Call Call
	Key  string
}

// DeleteKV authorizes and deletes a key under the shared write lock.
func (s *Service) DeleteKV(ctx context.Context, in KVDeleteInput) (*Error, error) {
	if auth := s.authorize(ctx, &in.Call, "kv", ruleengine.OpDelete); auth != nil {
		return auth, nil
	}
	n, writer, err := s.deleteKVSQLite(ctx, in.Call, in.Key)
	if err != nil {
		return nil, err
	}
	name, data := kvDeleteNotification(in.Key, n)
	if name != "" {
		// writer (captured under the write lock, just before the delete)
		// is the deleted key's own author. This is what lets web-push
		// target that visitor the way row_write.go's delete already does.
		data["owner_write"] = in.Call.Owner
		if writer != "" {
			data["row_visitor_id"] = writer
		}
		s.Emit(Event{Call: in.Call, Name: name, Data: data})
	}
	return nil, nil
}
