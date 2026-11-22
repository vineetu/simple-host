package data

import (
	"context"
	"errors"
	"net/http"
	"os"

	"github.com/vsriram/simple-host/host/access/auth"
	httpx "github.com/vsriram/simple-host/host/httpx"
)

func (h *Handler) StorageSvc() *Service {
	return New(h.Database, h.StorageDeps())
}

// SetStorageBindings is called once at startup. A malformed product schema is a
// programming/configuration error and stops startup before any request is served.
func (h *Handler) SetStorageBindings(p BindingProvider) {
	declaration := SnapshotBindings(p)
	schema, err := MergeBindingSchema(h.turnstile != nil, declaration)
	if err != nil {
		panic(err)
	}
	// Nil keeps the platform's own rules (data.DefaultProvider); a product
	// provider (Simple Hack) switches the product-only storage behaviour.
	h.storageBindings = p
	h.storageBindingSchema = schema
	h.storageProductBindingSchema = BindingSchema{}
	if declaration != nil {
		h.storageProductBindingSchema = declaration.Schema()
	}
}

func (h *Handler) StorageDeps() Deps {
	return Deps{
		Bindings:             h.storageBindings,
		BindingSchema:        h.storageBindingSchema,
		ProductBindingSchema: h.storageProductBindingSchema,
		SpamCheckAvailable:   h.turnstile != nil,
		Disk:                 h.Disk,
		SiteSlots:            h.SqliteSites,
		Locks:                h.UploadLocks, CombinedPool: h.Ed.Storage().CombinedPool,
		SiteLimit: os.Getenv("SITE_STORAGE_MAX_BYTES"), FilesLimit: os.Getenv("SITE_STORAGE_FILES_MAX_BYTES"),
		SpamCheckedMaxBytes: h.Limits.SpamCheckedMaxBytes, SpamCheckedMaxLinks: h.Limits.SpamCheckedMaxLinks,
		VisitorSessionIdle: h.Limits.VisitorSessionIdle,
		ExportKey:          h.ExportKey,
	}
}

// StorageFor is the storage service for one call: its SQLite database, the
// timeouts of the caller's role, the write lock's resource recheck and where
// a committed write's event goes.
func (h *Handler) StorageFor(c Call) *Service {
	deps := h.StorageDeps()
	deps.SQLitePath = h.StorageSQLPath(c)
	deps.SQLiteGate = h.Sqlite
	deps.SQLiteTimeout, deps.WriteTimeout = h.Limits.StorageVisitorQueryTimeout, h.Limits.StorageVisitorWriteTimeout
	if c.Owner {
		deps.SQLiteTimeout, deps.WriteTimeout = h.Limits.StorageOwnerTimeout, h.Limits.StorageOwnerTimeout
	}
	deps.WriteLockWait, deps.RecheckResource = h.Limits.StorageWriteLockWait, true
	deps.FileObjectsLimit = h.Limits.StorageFileObjects
	deps.Notifications, deps.Hosts = h.Notifier(), h.Hosts
	return New(h.Database, deps)
}

// WriteStorageError is the one storage refusal writer: a code answers at
// httpx.RefusalStatus (fallback where the table is silent; 0 makes an unknown
// code, or an error that is not a refusal, the generic 500).
func WriteStorageError(w http.ResponseWriter, err error, fallback int) {
	var e *Error
	status := 0
	if errors.As(err, &e) {
		status = httpx.RefusalStatus(e.Code, fallback)
	}
	switch {
	case status == 0:
		httpx.WriteError(w, 500, "internal_error", "internal server error")
	case e.Code == "account_suspended":
		auth.WriteSuspended(w, "", "")
	default:
		if e.Code == "storage_busy" && !e.NoRetryAfter {
			w.Header().Set("Retry-After", "1")
		}
		message := e.Message
		if message == "" {
			message = turnstileMessages[e.Code]
		}
		httpx.WriteJSON(w, status, httpx.ErrorResponse{Error: message, Code: e.Code, RuleError: e.RuleError, RulesErrors: e.RulesErrors})
	}
}

// storageAnswered answers a refusal or a failure; true when the request
// stops there.
func StorageAnswered(w http.ResponseWriter, auth *Error, err error, fallback int) bool {
	if storageRefused(w, auth) {
		return true
	}
	if err != nil {
		WriteStorageError(w, err, fallback)
		return true
	}
	return false
}

// storageRefused answers an authorization refusal (403 where its code says
// nothing else); true when the request stops there.
func storageRefused(w http.ResponseWriter, a *Error) bool {
	if a == nil {
		return false
	}
	if len(a.Refusals) == 0 {
		WriteStorageError(w, a, 403)
		return true
	}
	for _, e := range a.Refusals {
		WriteStorageError(w, e, 403)
	}
	return !a.Allowed
}

// KVFileStats shares request admission with the paper collector.
func (h *Handler) KVFileStats(ctx context.Context, siteID string) (int64, int64, error) {
	return h.StorageSvc().SiteKVFileStats(ctx, siteID)
}
