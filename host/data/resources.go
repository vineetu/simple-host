package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"regexp"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/data/repo"
	"github.com/vsriram/simple-host/host/model"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// ResourceListing contains a resource and its rule errors.
type ResourceListing struct {
	Resource    model.Resource
	RulesErrors []ruleengine.RuleError
}

// ListStorageResources constructs the owner's resource listing in query order.
func (s *Service) ListStorageResources(ctx context.Context, call Call) ([]ResourceListing, error) {
	if !call.Owner {
		return nil, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	list, err := repo.ListResources(ctx, s.db, call.SiteID)
	if err != nil {
		return nil, err
	}
	out := []ResourceListing{}
	for _, x := range list {
		out = append(out, ResourceListing{Resource: x, RulesErrors: s.resourceRulesErrors(x)})
	}
	return out, nil
}

// ResourceInput holds the decoded resource configuration: rules for every
// kind (and, for SQLite, Tables of rules). PresetFieldsPresent records any
// field of the removed presets, which is refused.
type ResourceInput struct {
	Call                     Call
	Name, Kind, SitePasscode string
	Rules                    *json.RawMessage
	PresetFieldsPresent      bool
	Tables                   *map[string]ResourceTableInput
}

// ResourceTableInput is a table override in a resource save: rules only.
// PresetFieldsPresent records any preset field sent, which is refused.
type ResourceTableInput struct {
	Rules               *json.RawMessage
	PresetFieldsPresent bool
}

// ResourceResult holds the configured resource response.
type ResourceResult struct {
	Resource    model.Resource
	RulesErrors []ruleengine.RuleError
	Warnings    []string
	Created     bool
}

var resourceTableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// PutStorageResource updates a resource while holding the site write lock.
func (s *Service) PutStorageResource(ctx context.Context, in ResourceInput) (ResourceResult, error) {
	call := in.Call
	if !call.Owner {
		return ResourceResult{}, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	ctx, unlock, err := s.writeLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, nil)
	if err != nil {
		return ResourceResult{}, err
	}
	defer unlock()
	internal := &Error{Code: "internal_error", Message: "internal server error"}
	old, err := repo.LoadResource(ctx, s.db, call.SiteID, in.Name)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ResourceResult{}, internal
	}
	x, err := in.resource(old, exists)
	if err != nil {
		return ResourceResult{}, err
	}
	rulesErrors, err := s.applyResourceRules(ctx, in, old, exists, &x)
	if err != nil {
		return ResourceResult{}, storageFailure(err, internal)
	}
	if !exists && x.Kind == "kv" {
		if err = s.createKVSQLite(ctx, call, x.Name); err != nil {
			return ResourceResult{}, storageFailure(err, internal)
		}
	}
	// Before presets a visitor's overwrite in a full-mode KV or files
	// resource recorded the overwriter, so a later own would hand them
	// someone else's key. The first rules forget those writers.
	clearWriters := exists && x.Rules != nil && old.BeforePresets && old.WriteMode == "full" && old.Write != model.WhoOwner
	if err = s.storeResource(ctx, call, x, clearWriters); err != nil {
		return ResourceResult{}, storageFailure(err, internal)
	}
	unlock()
	return ResourceResult{Resource: x, RulesErrors: rulesErrors, Created: !exists}, nil
}

var resourceKinds = map[string]bool{"kv": true, "sqlite": true, "files": true}

// resource validates the save's kind, passcode and shape against the stored
// resource and returns the resource it describes, rules not yet set.
func (in ResourceInput) resource(old model.Resource, exists bool) (model.Resource, error) {
	x := model.Resource{Name: in.Name, Kind: in.Kind, SitePasscode: in.SitePasscode}
	if x.SitePasscode == "" {
		x.SitePasscode = "inherit"
	}
	switch {
	case !resourceKinds[x.Kind] || x.SitePasscode != "inherit" && x.SitePasscode != "off":
		return x, &Error{Code: "invalid_resource", Message: "kind takes kv, sqlite, or files; site_passcode takes inherit or off"}
	case exists && old.Kind != x.Kind:
		return x, &Error{Code: "resource_kind_conflict", Message: "resource kind cannot change"}
	case in.hasPresetFields():
		return x, &Error{Code: "invalid_resource", Message: "presets were removed; send rules"}
	case !hasRules(in.Rules) && in.Tables != nil:
		return x, invalidRules("shape", "", "table rules require resource rules")
	}
	return x, nil
}

// applyResourceRules sets x's rules. A save without rules (a site_passcode
// change) keeps the stored rules and reports their current errors; a new
// resource without rules is owner only.
func (s *Service) applyResourceRules(ctx context.Context, in ResourceInput, old model.Resource, exists bool, x *model.Resource) ([]ruleengine.RuleError, error) {
	if !hasRules(in.Rules) && exists {
		passcode := x.SitePasscode
		*x = old
		x.SitePasscode = passcode
		return s.retainedRulesErrors(ctx, in.Call, *x)
	}
	if !hasRules(in.Rules) {
		owner := model.OwnerOnlyRules
		in.Rules = &owner
	}
	return nil, s.configureRules(ctx, in, old, exists, x)
}

// storeResource saves the resource and its table rules, first forgetting the
// recorded writers when clearWriters is set. A KV namespace's writers live in
// its SQLite file, whose clearing commits right before the Postgres commit, so
// a policy is never saved over writers that were not cleared.
func (s *Service) storeResource(ctx context.Context, call Call, x model.Resource, clearWriters bool) error {
	if clearWriters && x.Kind == "kv" {
		return s.clearKVWritersSQLite(ctx, call, x.Name, func(commitFile func() error) error {
			return s.upsertResource(ctx, call, x, false, commitFile)
		})
	}
	return s.upsertResource(ctx, call, x, clearWriters && x.Kind == "files", nil)
}

// upsertResource writes the resource row and replaces its table rules in one
// transaction; commitFile, when set, runs right before the commit.
func (s *Service) upsertResource(ctx context.Context, call Call, x model.Resource, clearFileWriters bool, commitFile func() error) error {
	internal := &Error{Code: "internal_error", Message: "internal server error"}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internal
	}
	defer tx.Rollback()
	if clearFileWriters {
		if _, err := repo.ClearFileWriters(ctx, tx, call.SiteID, x.Name); err != nil {
			return internal
		}
	}
	result, err := repo.UpsertResource(ctx, tx, call.SiteID, x)
	if err != nil {
		return internal
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return &Error{Code: "resource_kind_conflict", Message: "resource kind cannot change"}
	}
	if _, err = repo.DeleteResourceTables(ctx, tx, call.SiteID, x.Name); err != nil {
		return internal
	}
	for table, t := range x.Tables {
		if _, err = repo.InsertResourceTable(ctx, tx, call.SiteID, x.Name, table, t); err != nil {
			return internal
		}
	}
	if commitFile != nil {
		if err = commitFile(); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return internal
	}
	return nil
}

// storageFailure keeps a service error and names busy or full storage;
// anything else is reported as fallback.
func storageFailure(err error, fallback *Error) *Error {
	var e *Error
	switch {
	case errors.As(err, &e):
		return e
	case errors.Is(err, runtime.ErrSQLiteBusy):
		return sqliteBusy()
	case errors.Is(err, sqlite3.FULL):
		return kvFull()
	}
	return fallback
}

// DeleteStorageResource removes a resource and its staged runtime files.
func (s *Service) DeleteStorageResource(ctx context.Context, call Call) error {
	if !call.Owner {
		return &Error{Code: "forbidden", Message: "owner credential required"}
	}
	ctx, unlock, err := s.writeLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return err
	}
	defer unlock()
	staged, restore, err := runtime.StageRuntime(s.deps.Disk, call.OwnerID, call.SiteName, call.ResourceName, call.Resource.Kind)
	if err != nil {
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		restore()
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	if err = repo.DeleteResource(ctx, tx, call.SiteID, call.ResourceName); err != nil {
		_ = tx.Rollback()
		restore()
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	if err = tx.Commit(); err != nil {
		restore()
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	if staged != "" {
		if err = os.RemoveAll(staged); err != nil {
			return &Error{Code: "internal_error", Message: "resource removed but staging cleanup failed"}
		}
	}
	s.deps.MarkChanged()
	unlock()
	return nil
}
