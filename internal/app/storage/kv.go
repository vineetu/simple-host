package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	repository "github.com/vsriram/simple-host/internal/repository/storage"
	"strings"
	"time"

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

// putKV applies the quota check and writes an authorized key while its caller
// holds the site write lock.
func (s *Service) putKV(ctx context.Context, siteID, resource, root, key string, value json.RawMessage, filter string, owner bool, visitorID string, exists bool, committed func(context.Context)) error {
	usage, err := s.MeasureSiteStorage(ctx, siteID, root)
	if err != nil {
		return err
	}
	old, err := repository.KVSize(ctx, s.db, siteID, resource, key)
	if err != nil {
		return err
	}
	normalized, err := repository.KVNormalizedSize(ctx, s.db, value)
	if err != nil {
		return &Error{Code: "invalid_value", Message: "invalid JSON value"}
	}
	if s.BudgetUsed(usage)-old+normalized+int64(len(key)) > SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool) {
		return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
	}
	var result sql.Result
	if exists {
		result, err = repository.UpdateKV(ctx, s.db, siteID, resource, key, value, filter)
	} else {
		result, err = repository.InsertKV(ctx, s.db, siteID, resource, key, value, StampID(owner, visitorID))
	}
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return &Error{Code: "key_exists", Message: "this key already exists"}
	}
	committed(ctx)
	return nil
}

// WriteCheckedKV checks the resolved author before completing an authorized write.
func (s *Service) WriteCheckedKV(ctx context.Context, unlock func(), siteID, resource, root, key string, value json.RawMessage, filter string, owner bool, visitorID, writer string, exists bool, checkedPost bool, maxBytes, maxLinks int, committed func(context.Context), notify func(string, map[string]any)) error {
	if err := checkKVAuthor(exists, writer, filter); err != nil {
		return err
	}
	if checkedPost {
		if err := validateKVPost(value, maxBytes, maxLinks); err != nil {
			return err
		}
	}
	if err := s.putKV(ctx, siteID, resource, root, key, value, filter, owner, visitorID, exists, committed); err != nil {
		return err
	}
	name, data := finishKVWrite(unlock, key, value, exists)
	notify(name, data)
	return nil
}

// validateKVPost applies the existing checked-post limits.
func validateKVPost(value json.RawMessage, maxBytes, maxLinks int) error {
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
func (s *Service) prepareKVWrite(ctx context.Context, siteID, resource, key, requested string, taken bool) (string, bool, string, error) {
	writer, err := repository.KVWriter(ctx, s.db, siteID, resource, key)
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
	action, err := kvAction(exists, exists && taken)
	return writer, exists, action, err
}

// kvAction chooses the action for a key and rejects an inaccessible existing key.
func kvAction(exists, taken bool) (string, error) {
	if !exists {
		return "add", nil
	}
	if taken {
		return "", &Error{Code: "key_exists", Message: "this key already exists"}
	}
	return "edit", nil
}

// checkKVAuthor rejects an edit hidden by the resolved author filter.
func checkKVAuthor(exists bool, writer, filter string) error {
	if exists && filter != "" && writer != filter {
		return &Error{Code: "key_exists", Message: "this key already exists"}
	}
	return nil
}

// InsertKV creates a key if it does not already exist.
func (s *Service) InsertKV(ctx context.Context, siteID, resource, key string, value json.RawMessage, owner bool, visitorID string) (bool, error) {
	result, err := repository.InsertKV(ctx, s.db, siteID, resource, key, value, StampID(owner, visitorID))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// DeleteKV removes a visible key.
func (s *Service) deleteKV(ctx context.Context, siteID, resource, key, filter string) (sql.Result, error) {
	return repository.DeleteKV(ctx, s.db, siteID, resource, key, filter)
}

// getKV reads one key subject to the resolved author filter.
func (s *Service) getKV(ctx context.Context, siteID, resource, key, filter string) (repository.KVItem, error) {
	item, err := repository.GetKV(ctx, s.db, siteID, resource, key, filter)
	if errors.Is(err, sql.ErrNoRows) {
		return item, &Error{Code: "key_not_found", Message: "key not found"}
	}
	return item, err
}

// listKV reads a page of keys subject to the resolved author filter.
func (s *Service) listKV(ctx context.Context, siteID, resource, prefix, after string, limit int, filter string) ([]repository.KVItem, string, error) {
	rows, err := repository.ListKV(ctx, s.db, siteID, resource, prefix, after, limit+1, filter)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) > limit {
		next = rows[limit-1].Key
		rows = rows[:limit]
	}
	return rows, next, nil
}

// deleteKVLocked removes a key under the shared site lock after rechecking its resource.
func (s *Service) deleteKVLocked(ctx context.Context, lockKey string, wait, timeout time.Duration, recheck func(context.Context) bool, committed func(context.Context), siteID, resource, key, filter string) (int64, error) {
	ctx, unlock, err := s.WriteLock(ctx, lockKey, wait, timeout, recheck)
	if err != nil {
		return 0, err
	}
	defer unlock()
	result, err := s.deleteKV(ctx, siteID, resource, key, filter)
	if err != nil {
		return 0, err
	}
	n, countErr := result.RowsAffected()
	if filter != "" && (countErr != nil || n == 0) {
		return 0, &Error{Code: "key_not_found", Message: "key not found"}
	}
	committed(ctx)
	n, countErr = result.RowsAffected()
	if countErr != nil {
		return 0, nil
	}
	return n, nil
}

// DeleteKVWithNotification deletes under the site lock and chooses the notification after release.
func (s *Service) DeleteKVWithNotification(ctx context.Context, lockKey string, wait, timeout time.Duration, recheck func(context.Context) bool, committed func(context.Context), siteID, resource, key, filter string, notify func(string, map[string]any)) error {
	n, err := s.deleteKVLocked(ctx, lockKey, wait, timeout, recheck, committed, siteID, resource, key, filter)
	if err != nil {
		return err
	}
	name, data := kvDeleteNotification(key, n)
	if name != "" {
		notify(name, data)
	}
	return nil
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

// BeginKVWrite locks and resolves a write before handler authorization.
func (s *Service) BeginKVWrite(ctx context.Context, lockKey string, wait, timeout time.Duration, recheck func(context.Context) bool, siteID, resource, key, requested string, visitorAI, taken bool) (context.Context, func(), string, bool, string, error) {
	if !visitorAI {
		requested = ""
	}
	ctx, unlock, err := s.WriteLock(ctx, lockKey, wait, timeout, recheck)
	if err != nil {
		return ctx, nil, "", false, "", err
	}
	writer, exists, action, err := s.prepareKVWrite(ctx, siteID, resource, key, requested, taken)
	if err != nil {
		unlock()
		return ctx, nil, "", false, "", err
	}
	return ctx, unlock, writer, exists, action, nil
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
func (s *Service) ListVisibleKV(ctx context.Context, siteID, resource, prefix, after string, limit int, filter, visitorID string, owner bool) ([]VisibleKVItem, string, error) {
	rows, next, err := s.listKV(ctx, siteID, resource, prefix, after, limit, filter)
	if err != nil {
		return nil, "", err
	}
	out := make([]VisibleKVItem, 0, len(rows))
	for _, row := range rows {
		writer, mine := kvAuthorVisibility(row.WriterID, visitorID, owner)
		out = append(out, VisibleKVItem{Key: row.Key, Value: row.Value, VisitorID: writer, Mine: mine})
	}
	return out, next, nil
}

// GetVisibleKV reads a key and resolves its author visibility.
func (s *Service) GetVisibleKV(ctx context.Context, siteID, resource, key, filter, visitorID string, owner bool) (VisibleKVItem, error) {
	item, err := s.getKV(ctx, siteID, resource, key, filter)
	if err != nil {
		return VisibleKVItem{}, err
	}
	writer, mine := kvAuthorVisibility(item.WriterID, visitorID, owner)
	return VisibleKVItem{Key: item.Key, Value: item.Value, VisitorID: writer, Mine: mine}, nil
}
