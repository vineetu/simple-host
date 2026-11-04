package handler

import (
	"context"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/db"
	"net/http"
	"os"
	"time"

	httpx "github.com/vsriram/simple-host/internal/httpx"
)
import appstorage "github.com/vsriram/simple-host/internal/app/storage"
import rules "github.com/vsriram/simple-host/internal/rules/storage"

func (h *SiteHandler) storageSvc() *appstorage.Service {
	return appstorage.New(h.database, h.storageDeps())
}

func (h *SiteHandler) storageDeps() appstorage.Deps {
	deps := appstorage.Deps{
		Locks: &h.uploadLocks, CombinedPool: h.ed.Storage().CombinedPool,
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
	deps.EventOwners = h.ed.Storage().EventOwners
	deps.LegacyRoutes = h.ed.Storage().LegacyRoutes
	deps.ResourceHistory = func(ctx context.Context, siteID, name string) (any, error) {
		return db.ListHistory(ctx, h.database, siteID, db.HistoryStorage, name, 20, 0)
	}
	deps.FilePath = func(call appstorage.Call, path string) string {
		return storageFileOnDisk(h.disk, call.OwnerID, call.SiteName, call.ResourceName, path)
	}
	return deps
}

// storageRequestSvc configures request-dependent checks for the ordered gate.
func (h *SiteHandler) storageRequestSvc(r *http.Request, c storageCall) *appstorage.Service {
	return appstorage.New(h.database, h.storageRequestDeps(r, c))
}

func (h *SiteHandler) storageRequestDeps(r *http.Request, c storageCall) appstorage.Deps {
	deps := h.storageDeps()
	deps.StageResourceRuntime = func() (string, func(), error) { return h.stageStorageRuntime(c) }
	deps.SpamCheckAvailable = h.turnstile != nil
	deps.SQLitePath = h.storageSQLPath(c)
	deps.SQLiteGate = h.sqliteGateFor()
	deps.SQLiteTimeout = h.limits.StorageVisitorQueryTimeout
	if c.owner {
		deps.SQLiteTimeout = h.limits.StorageOwnerTimeout
	}
	deps.SQLiteMaxMemory = storageSQLiteMaxMemory
	deps.PasscodeAllows = func() bool { return h.PasscodeLetsIn(r, c.siteID) }
	deps.ViewersAllow = func() bool { return h.ViewersLetIn(r, c.siteID) }
	deps.WriteLockWait = h.limits.StorageWriteLockWait
	deps.WriteTimeout = h.limits.StorageVisitorWriteTimeout
	if c.owner {
		deps.WriteTimeout = h.limits.StorageOwnerTimeout
	}
	deps.Recheck = func(ctx context.Context) bool {
		current, err := h.loadStorageResource(ctx, c.siteID, c.resourceName)
		return err == nil && current.fingerprint() == c.resource.fingerprint()
	}
	deps.Committed = operations.HackWriteCommitted
	deps.FilePath = func(_ appstorage.Call, path string) string { return h.storageFilePath(c, path) }
	deps.FileToken = func(_ appstorage.Call, path string, expires time.Time) string {
		return h.storageFileToken(c, path, expires)
	}
	deps.MarkChanged = h.disk.MarkChanged
	deps.FileObjectsLimit = h.limits.StorageFileObjects
	deps.ScanFile = func(_ appstorage.Call, path string, body []byte) {
		h.imageScan.EnqueueStorageFile(c.siteID, c.resourceName, path, storageUploader(c), body)
	}
	deps.Notify = func(_ appstorage.Call, name string, data map[string]any) { h.emitNotification(c, name, data) }
	return deps
}

// storageServiceCall converts resolved handler state to plain service values.
func (h *SiteHandler) storageServiceCall(c storageCall) appstorage.Call {
	return appstorage.Call{
		SiteID: c.siteID, OwnerID: c.ownerID, SiteName: c.siteName, ResourceName: c.resourceName,
		AccountID: c.accountID, Owner: c.owner, OwnerOnSite: c.ownerOnSite, VisitorAI: c.visitorAI,
		VisitorID: c.visitorID, LinkScope: c.linkScope, Resource: c.resource.rulesResource(),
		ActiveChecked: c.activeChecked, CurrentMatrix: rules.AccessMatrix(c.currentMatrix),
		Filter: c.filter, SpamCheck: c.spamCheck, CheckedPost: c.checkedPost,
		UploaderID: c.uploaderID, RuntimeDir: h.storageRuntimeDir(c),
		VisitorSessionID: c.visitorSession.ID, VisitorSessionUserID: c.visitorSession.UserID,
	}
}

// writeStorageRefusals preserves the order of responses produced by session checks.
func writeStorageRefusals(w http.ResponseWriter, refusals []*appstorage.Error) {
	for _, e := range refusals {
		switch e.Code {
		case "account_suspended":
			writeAccountSuspended(w)
		case "internal_error":
			storageError(w, 500, e.Code, e.Message)
		case "sign_in_required":
			storageError(w, 401, e.Code, e.Message)
		default:
			message := e.Message
			if message == "" {
				message = turnstileMessages[e.Code]
			}
			storageError(w, 403, e.Code, message)
		}
	}
}
