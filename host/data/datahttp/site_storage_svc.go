package datahttp

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/papermetrics"

	"github.com/vsriram/simple-host/host/access/auth"
	appstorage "github.com/vsriram/simple-host/host/data"
	httpx "github.com/vsriram/simple-host/host/httpx"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

func (h *DataHandler) StorageSvc() *appstorage.Service {
	return appstorage.New(h.Database, h.StorageDeps())
}

// SetStorageBindings is called once at startup. A malformed product schema is a
// programming/configuration error and stops startup before any request is served.
func (h *DataHandler) SetStorageBindings(p appstorage.BindingProvider) {
	declaration := appstorage.SnapshotBindings(p)
	schema, err := appstorage.MergeBindingSchema(h.turnstile != nil, declaration)
	if err != nil {
		panic(err)
	}
	// Nil keeps the platform's own rules (storageBindingProvider); a product
	// provider (Simple Hack) switches the product-only storage behaviour.
	h.storageBindings = p
	h.storageBindingSchema = schema
	h.storageProductBindingSchema = appstorage.BindingSchema{}
	if declaration != nil {
		h.storageProductBindingSchema = declaration.Schema()
	}
}

func (h *DataHandler) storageBindingProvider() appstorage.BindingProvider {
	if h.storageBindings != nil {
		return h.storageBindings
	}
	return appstorage.DefaultProvider{DB: h.Database}
}

func (h *DataHandler) StorageDeps() appstorage.Deps {
	deps := appstorage.Deps{
		Bindings:             h.storageBindingProvider(),
		BindingSchema:        h.storageBindingSchema,
		ProductBindingSchema: h.storageProductBindingSchema,
		SpamCheckAvailable:   h.turnstile != nil,
		SiteSlots:            h.SqliteSites,
		Locks:                h.UploadLocks, CombinedPool: h.Ed.Storage().CombinedPool,
		SiteLimit: os.Getenv("SITE_STORAGE_MAX_BYTES"), FilesLimit: os.Getenv("SITE_STORAGE_FILES_MAX_BYTES"),
		SpamCheckedMaxBytes: h.Limits.SpamCheckedMaxBytes, SpamCheckedMaxLinks: h.Limits.SpamCheckedMaxLinks,
		VisitorSessionIdle: h.Limits.VisitorSessionIdle,
	}
	deps.VisitorEmail = func(ctx context.Context, id string) (string, error) {
		return accountapp.VisitorEmail(ctx, h.Database, id)
	}
	deps.OpenFileToken = func(token string) ([]byte, error) { return httpx.OpenLink(h.ExportKey, "site storage file v1", token) }
	deps.FileResourceExists = func(ctx context.Context, siteID, name string) bool {
		resource, err := h.LoadStorageResource(ctx, siteID, name)
		return err == nil && resource.Kind == "files"
	}
	deps.FilePath = func(call appstorage.Call, path string) string {
		return storageFileOnDisk(h.Disk, call.OwnerID, call.SiteName, call.ResourceName, path)
	}
	return deps
}

// StorageFor is the storage service for one call: its SQLite database, the
// timeouts of the caller's role, the write lock's resource recheck and the
// effects of a committed write.
func (h *DataHandler) StorageFor(c StorageCall) *appstorage.Service {
	deps := h.StorageDeps()
	deps.Disk = h.Disk
	deps.SQLitePath = h.StorageSQLPath(c)
	deps.SQLiteGate = h.Sqlite
	deps.SQLiteTimeout, deps.WriteTimeout = h.Limits.StorageVisitorQueryTimeout, h.Limits.StorageVisitorWriteTimeout
	if c.Owner {
		deps.SQLiteTimeout, deps.WriteTimeout = h.Limits.StorageOwnerTimeout, h.Limits.StorageOwnerTimeout
	}
	deps.WriteLockWait = h.Limits.StorageWriteLockWait
	deps.Recheck = func(ctx context.Context) bool {
		current, err := h.LoadStorageResource(ctx, c.SiteID, c.ResourceName)
		return err == nil && current.Fingerprint() == c.Resource.Fingerprint()
	}
	deps.Committed = func(ctx context.Context) { papermetrics.From(ctx).WriteCommitted(ctx) }
	deps.FileToken = func(_ appstorage.Call, path string, expires time.Time) string {
		return h.StorageFileToken(c, path, expires)
	}
	deps.MarkChanged = h.Disk.MarkChanged
	deps.FileObjectsLimit = h.Limits.StorageFileObjects
	deps.Notify = func(_ appstorage.Call, name string, data map[string]any) { h.EmitNotification(c, name, data) }
	return appstorage.New(h.Database, deps)
}

// WriteStorageError is the one storage refusal writer: a code answers at
// httpx.RefusalStatus (fallback where the table is silent; 0 makes an unknown
// code, or an error that is not a refusal, the generic 500).
func WriteStorageError(w http.ResponseWriter, err error, fallback int) {
	var e *appstorage.Error
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
func storageAnswered(w http.ResponseWriter, auth *appstorage.Error, err error, fallback int) bool {
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
func storageRefused(w http.ResponseWriter, a *appstorage.Error) bool {
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

// KVFileStats shares request admission with the paper collector. Trashed sites
// retain their namespaces until permanent deletion, just as Postgres rows do.
func (h *DataHandler) KVFileStats(ctx context.Context, siteID string) (int64, int64, error) {
	site, err := sitesrepo.GetSiteByID(ctx, h.Database, siteID)
	if err != nil {
		return 0, 0, err
	}
	dir := h.Disk.SiteDir(site.UserID, site.Name)
	if site.Deleted {
		dir = h.Disk.TrashDir(site.UserID, site.ID)
	}
	return h.StorageSvc().KVFileStats(ctx, siteID, filepath.Join(dir, "runtime"))
}
