package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/data/repo"
	"github.com/vsriram/simple-host/host/data/rules"
	"github.com/vsriram/simple-host/host/model"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// ResourceListing contains a resource and its rule errors.
type ResourceListing struct {
	Resource    rules.Resource
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
	Resource    rules.Resource
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
	name := in.Name
	old, e := repo.LoadResource(ctx, s.db, call.SiteID, in.Name)
	exists := e == nil
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
	}
	x := rules.Resource{Name: name, Kind: in.Kind, SitePasscode: in.SitePasscode}
	if x.SitePasscode == "" {
		x.SitePasscode = "inherit"
	}
	if x.Kind != "kv" && x.Kind != "sqlite" && x.Kind != "files" || x.SitePasscode != "inherit" && x.SitePasscode != "off" {
		return ResourceResult{}, &Error{Code: "invalid_resource", Message: "kind takes kv, sqlite, or files; site_passcode takes inherit or off"}
	}
	if exists && old.Kind != x.Kind {
		return ResourceResult{}, &Error{Code: "resource_kind_conflict", Message: "resource kind cannot change"}
	}
	var warnings []string
	var rulesErrors []ruleengine.RuleError
	ruleFailure := func(err error) error {
		if e, ok := err.(*Error); ok {
			return e
		}
		if errors.Is(err, runtime.ErrSQLiteBusy) {
			return &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
		}
		return &Error{Code: "internal_error", Message: "internal server error"}
	}
	switch {
	case in.hasPresetFields():
		return ResourceResult{}, &Error{Code: "invalid_resource", Message: "presets were removed; send rules"}
	case !hasRules(in.Rules) && in.Tables != nil:
		return ResourceResult{}, invalidRules("shape", "", "table rules require resource rules")
	case !hasRules(in.Rules) && exists:
		// A save without rules (a site_passcode change) keeps the stored rules.
		passcode := x.SitePasscode
		x = old
		x.SitePasscode = passcode
		if rulesErrors, err = s.retainedRulesErrors(ctx, call, x); err != nil {
			return ResourceResult{}, ruleFailure(err)
		}
	default:
		if !hasRules(in.Rules) {
			// A new resource without rules is owner only.
			owner := model.OwnerOnlyRules
			in.Rules = &owner
		}
		if err := s.configureRules(ctx, in, old, exists, &x); err != nil {
			if errors.Is(err, sqlite3.FULL) {
				return ResourceResult{}, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
			}
			return ResourceResult{}, ruleFailure(err)
		}
	}
	if !exists && x.Kind == "kv" {
		if err := s.createKVSQLite(ctx, call, x.Name); err != nil {
			var e *Error
			if errors.As(err, &e) {
				return ResourceResult{}, e
			}
			return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
	}
	internal := &Error{Code: "internal_error", Message: "internal server error"}
	// Before presets a visitor's overwrite in a full-mode KV or files
	// resource recorded the overwriter, so a later own would hand them
	// someone else's key. The first rules forget those writers.
	clearWriters := exists && x.Rules != nil && old.BeforePresets && old.WriteMode == "full" && old.Write != model.WhoOwner
	// commitFile, when set, commits the same clearing in the namespace's
	// SQLite file. It runs right before the Postgres commit, so a policy is
	// never saved over writers that were not cleared.
	save := func(commitFile func() error) error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return internal
		}
		defer tx.Rollback()
		if clearWriters && x.Kind == "files" {
			if _, e := repo.ClearFileWriters(ctx, tx, call.SiteID, name); e != nil {
				return internal
			}
		}
		result, e := repo.UpsertResource(ctx, tx, call.SiteID, x)
		if e != nil {
			return internal
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			return &Error{Code: "resource_kind_conflict", Message: "resource kind cannot change"}
		}
		if _, e = repo.DeleteResourceTables(ctx, tx, call.SiteID, name); e != nil {
			return internal
		}
		for table, t := range x.Tables {
			if _, e = repo.InsertResourceTable(ctx, tx, call.SiteID, name, table, t); e != nil {
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
	if clearWriters && x.Kind == "kv" {
		err = s.clearKVWritersSQLite(ctx, call, name, save)
	} else {
		err = save(nil)
	}
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			return ResourceResult{}, e
		}
		if errors.Is(err, runtime.ErrSQLiteBusy) {
			return ResourceResult{}, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
		}
		return ResourceResult{}, internal
	}

	unlock()
	if len(warnings) > 0 {
		sort.Strings(warnings)
	}
	return ResourceResult{Resource: x, RulesErrors: rulesErrors, Warnings: warnings, Created: !exists}, nil
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
		if err = runtime.RemoveStagedRuntime(staged); err != nil {
			return &Error{Code: "internal_error", Message: "resource removed but staging cleanup failed"}
		}
	}
	s.deps.MarkChanged()
	unlock()
	return nil
}
