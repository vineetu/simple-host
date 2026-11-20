package handler

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	appstorage "github.com/vsriram/simple-host/host/data"
	httpx "github.com/vsriram/simple-host/host/httpx"
)

func (h *DataHandler) storageSvc() *appstorage.Service {
	return appstorage.New(h.database, h.storageDeps())
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
	return appstorage.DefaultProvider{DB: h.database}
}

func (h *DataHandler) storageDeps() appstorage.Deps {
	deps := appstorage.Deps{
		Bindings:             h.storageBindingProvider(),
		BindingSchema:        h.storageBindingSchema,
		ProductBindingSchema: h.storageProductBindingSchema,
		SpamCheckAvailable:   h.turnstile != nil,
		SiteSlots:            h.siteSlotsFor(),
		Locks:                h.uploadLocks, CombinedPool: h.ed.Storage().CombinedPool,
		SiteLimit: os.Getenv("SITE_STORAGE_MAX_BYTES"), FilesLimit: os.Getenv("SITE_STORAGE_FILES_MAX_BYTES"),
		SpamCheckedMaxBytes: h.limits.SpamCheckedMaxBytes, SpamCheckedMaxLinks: h.limits.SpamCheckedMaxLinks,
		VisitorSessionIdle: h.limits.VisitorSessionIdle,
	}
	deps.VisitorEmail = func(ctx context.Context, id string) (string, error) { return visitorEmail(ctx, h.database, id) }
	deps.OpenFileToken = func(token string) ([]byte, error) { return httpx.OpenLink(h.exportKey, "site storage file v1", token) }
	deps.FileResourceExists = func(ctx context.Context, siteID, name string) bool {
		resource, err := h.loadStorageResource(ctx, siteID, name)
		return err == nil && resource.Kind == "files"
	}
	deps.FilePath = func(call appstorage.Call, path string) string {
		return storageFileOnDisk(h.disk, call.OwnerID, call.SiteName, call.ResourceName, path)
	}
	return deps
}

// storageRequestSvc configures request-dependent checks for the ordered gate.
func (h *DataHandler) storageRequestSvc(r *http.Request, c storageCall) *appstorage.Service {
	return appstorage.New(h.database, h.storageRequestDeps(r, c))
}

func (h *DataHandler) storageRequestDeps(r *http.Request, c storageCall) appstorage.Deps {
	deps := h.storageDeps()
	deps.Disk = h.disk
	deps.SpamCheckAvailable = h.turnstile != nil
	deps.SQLitePath = h.storageSQLPath(c)
	deps.SQLiteGate = h.sqliteGateFor()
	deps.SQLiteTimeout = h.limits.StorageVisitorQueryTimeout
	if c.owner {
		deps.SQLiteTimeout = h.limits.StorageOwnerTimeout
	}
	deps.PasscodeAllows = func() bool { return h.access.PasscodeLetsIn(r, c.siteID) }
	deps.ViewersAllow = func() bool { return h.access.ViewersLetIn(r, c.siteID) }
	deps.WriteLockWait = h.limits.StorageWriteLockWait
	deps.WriteTimeout = h.limits.StorageVisitorWriteTimeout
	if c.owner {
		deps.WriteTimeout = h.limits.StorageOwnerTimeout
	}
	deps.Recheck = func(ctx context.Context) bool {
		current, err := h.loadStorageResource(ctx, c.siteID, c.resourceName)
		return err == nil && current.Fingerprint() == c.resource.Fingerprint()
	}
	deps.Committed = func(ctx context.Context) { paperRecorder(ctx).WriteCommitted(ctx) }
	deps.FilePath = func(_ appstorage.Call, path string) string { return h.storageFilePath(c, path) }
	deps.FileToken = func(_ appstorage.Call, path string, expires time.Time) string {
		return h.storageFileToken(c, path, expires)
	}
	deps.MarkChanged = h.disk.MarkChanged
	deps.FileObjectsLimit = h.limits.StorageFileObjects
	deps.Notify = func(_ appstorage.Call, name string, data map[string]any) { h.emitNotification(c, name, data) }
	return deps
}

// storageServiceCall converts resolved handler state to plain service values.
func (h *DataHandler) storageServiceCall(c storageCall) appstorage.Call {
	return appstorage.Call{
		SiteID: c.siteID, OwnerID: c.ownerID, SiteName: c.siteName, ResourceName: c.resourceName,
		AccountID: c.accountID, Owner: c.owner, OwnerOnSite: c.ownerOnSite, VisitorAI: c.visitorAI,
		VisitorID: c.visitorID, LinkScope: c.linkScope, Resource: c.resource,
		ActiveChecked: c.activeChecked,
		SpamCheck:     c.spamCheck, CheckedPost: c.checkedPost,
		RuntimeDir:       h.storageRuntimeDir(c),
		VisitorSessionID: c.visitorSession.ID, VisitorSessionUserID: c.visitorSession.UserID,
	}
}

// writeStorageError is the one storage refusal writer: a code answers at
// httpx.RefusalStatus (fallback where the table is silent; 0 makes an unknown
// code, or an error that is not a refusal, the generic 500).
func writeStorageError(w http.ResponseWriter, err error, fallback int) {
	var e *appstorage.Error
	status := 0
	if errors.As(err, &e) {
		status = httpx.RefusalStatus(e.Code, fallback)
	}
	switch {
	case status == 0:
		storageError(w, 500, "internal_error", "internal server error")
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
		writeJSON(w, status, errorResponse{Error: message, Code: e.Code, RuleError: e.RuleError, RulesErrors: e.RulesErrors})
	}
}

// storageRefused answers an authorization refusal (403 where its code says
// nothing else); true when the request stops there.
func storageRefused(w http.ResponseWriter, a *appstorage.Error) bool {
	if a == nil {
		return false
	}
	if len(a.Refusals) == 0 {
		writeStorageError(w, a, 403)
		return true
	}
	for _, e := range a.Refusals {
		writeStorageError(w, e, 403)
	}
	return !a.Allowed
}

func (h *DataHandler) siteSlotsFor() *appstorage.SiteSlots {
	h.sqliteSitesOnce.Do(func() {
		h.sqliteSites = appstorage.NewSiteSlots(h.limits.StorageSiteInflight, h.sqliteGateFor(), h.limits.StorageAcquireWait)
	})
	return h.sqliteSites
}

// KVFileStats shares request admission with the paper collector. Trashed sites
// retain their namespaces until permanent deletion, just as Postgres rows do.
func (h *DataHandler) KVFileStats(ctx context.Context, siteID string) (int64, int64, error) {
	site, err := h.site.lifecycleSvc().GetSiteByID(ctx, siteID)
	if err != nil {
		return 0, 0, err
	}
	dir := h.disk.SiteDir(site.UserID, site.Name)
	if site.Deleted {
		dir = h.disk.TrashDir(site.UserID, site.ID)
	}
	return h.storageSvc().KVFileStats(ctx, siteID, filepath.Join(dir, "runtime"))
}
