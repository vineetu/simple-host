package storage

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repo "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// ResourceListing contains a resource and optional legacy visitor history.
type ResourceListing struct {
	Resource          rules.Resource
	VisitorHistory    any
	HasVisitorHistory bool
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
		entry := ResourceListing{Resource: x}
		if s.deps.LegacyRoutes {
			history, err := s.deps.ResourceHistory(ctx, call.SiteID, x.Name)
			if err != nil {
				return nil, err
			}
			entry.VisitorHistory = history
			entry.HasVisitorHistory = true
		}
		out = append(out, entry)
	}
	return out, nil
}

// ResourceInput holds the decoded resource configuration.
type ResourceInput struct {
	Call       Call
	Name, Kind string
	rules.AccessRequest
	Write, WriteMode, SitePasscode string
	Tables                         *map[string]rules.AccessRequest
	ApproveFirst                   *bool
}

// ResourceResult holds the configured resource response.
type ResourceResult struct {
	Resource rules.Resource
	Warnings []string
	Created  bool
}

var resourceTableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func accessResourceError(rule, hint string) *Error {
	code := "invalid_access"
	if rule == "invalid_value" || rule == "invalid_preset" {
		code = "invalid_resource"
	}
	return &Error{Code: code, Message: hint, Rule: rule}
}

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
	var ok bool
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
	presetFields := in.Preset != "" || in.Add != "" || in.Edit != "" || in.Delete != "" || in.Tables != nil
	if presetFields && (in.Write != "" || in.WriteMode != "") {
		return ResourceResult{}, &Error{Code: "invalid_access", Message: "use preset (with read, add, edit, delete overrides), or the older write and write_mode, not both"}
	}
	var m rules.AccessMatrix
	var base, code, hint string
	if !presetFields && exists && len(old.Tables) > 0 {
		// The older fields cannot say anything about per-table presets, so
		// a client using them would think it set the whole database.
		return ResourceResult{}, &Error{Code: "tables_present", Message: "this database has per-table presets; set it with preset and tables (storage_list_resources shows them)"}
	}
	if in.Write != "" || in.WriteMode != "" {
		mode := in.WriteMode
		if mode == "" && exists {
			// An older client that leaves write_mode out keeps the mode.
			mode = old.WriteMode
			if old.Access != nil {
				if _, _, oldMode, ok := rules.OldFields(*old.Access); ok {
					mode = oldMode
				}
			}
		}
		m, code, hint = rules.TranslateOld(in.Read, in.Write, mode)
		if base = rules.PresetName(m); base == "custom" {
			base = ""
		}
	} else {
		m, base, code, hint = in.AccessRequest.Matrix()
	}
	if code != "" {
		return ResourceResult{}, accessResourceError(code, hint)
	}
	x.Access, x.PresetBase = &m, base
	x.Tables = map[string]rules.TableRule{}
	var warnings []string
	if in.Tables != nil {
		if x.Kind != "sqlite" {
			return ResourceResult{}, &Error{Code: "invalid_resource", Message: "tables apply to sqlite resources only"}
		}
		if len(*in.Tables) > 50 {
			return ResourceResult{}, &Error{Code: "invalid_resource", Message: "at most 50 tables have their own rules"}
		}
		for table, q := range *in.Tables {
			if !resourceTableNameRE.MatchString(table) || strings.HasPrefix(strings.ToLower(table), "sqlite_") {
				return ResourceResult{}, &Error{Code: "invalid_resource", Message: "table names are letters, digits, and _, starting with a letter"}
			}
			// SQLite table names are case-insensitive: one rule per table.
			table = strings.ToLower(table)
			if _, dup := x.Tables[table]; dup {
				return ResourceResult{}, &Error{Code: "invalid_resource", Message: "two rules name the same table"}
			}
			tm, tbase, tcode, thint := q.Matrix()
			if tcode != "" {
				return ResourceResult{}, accessResourceError(tcode, "table "+table+": "+thint)
			}
			x.Tables[table] = rules.TableRule{Matrix: tm, PresetBase: tbase}
		}
	} else if exists && old.Access != nil {
		x.Tables = old.Tables
	}
	if in.ApproveFirst != nil {
		x.ApproveFirst = *in.ApproveFirst
	} else if exists {
		x.ApproveFirst = old.ApproveFirst
	}
	if x.HasChecked() {
		if !s.deps.SpamCheckAvailable {
			return ResourceResult{}, &Error{Code: "spam_check_unavailable", Message: "the comments preset (add checked) needs the spam check, which this server does not offer; use wall (people sign in to post) or inbox"}
		}
		if x.Kind == "files" {
			return ResourceResult{}, &Error{Code: "invalid_access", Message: "checked (the comments preset) applies to kv and sqlite resources, not files; use wall for uploads"}
		}
	}
	if x.ApproveFirst && !x.HasChecked() {
		if in.ApproveFirst != nil {
			return ResourceResult{}, &Error{Code: "invalid_access", Message: "approve_first holds posts added through the spam check, so it needs the comments preset (add checked) on this resource or one of its tables"}
		}
		x.ApproveFirst = false
	}
	if x.ApproveFirst && x.Kind != "sqlite" {
		return ResourceResult{}, &Error{Code: "invalid_access", Message: "approve_first applies to sqlite resources only"}
	}
	x.Read, x.Write, x.WriteMode, ok = rules.OldFields(m)
	if !ok {
		x.Write, x.WriteMode = "owner", "add"
	}
	if x.Kind == "sqlite" {
		// Every table gets the server-owned visitor_id column, so own works
		// on any table later. Pages could write visitor_id freely on a legacy
		// database that took visitor SQL; such rows cannot become "own".
		usesOwn := m.UsesOwn()
		for _, t := range x.Tables {
			usesOwn = usesOwn || t.Matrix.UsesOwn()
		}
		pagesWroteSQL := exists && old.LegacyVisitorSQL() && old.Write != rules.WhoOwner
		requireEmpty := usesOwn && pagesWroteSQL

		// The first preset on a database pages wrote with SQL forgets every
		// visitor_id a page could have forged, so no later own applies to it.
		existing, err := s.prepareResourceMatrix(ctx, call, x, requireEmpty, pagesWroteSQL)
		for table := range x.Tables {
			if !existing[table] {
				warnings = append(warnings, "table "+table+" does not exist yet; its preset applies once it is created")
			}
		}
		if err != nil {
			if errors.Is(err, runtime.ErrSQLiteBusy) {
				return ResourceResult{}, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
			}
			if errors.Is(err, sqlite3.FULL) {
				return ResourceResult{}, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
			}
			return ResourceResult{}, &Error{Code: "visitor_id_required", Message: "every table needs visitor_id TEXT, set by the server; a database pages could write with SQL must be empty before own applies"}
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
	}
	defer tx.Rollback()
	// Before presets a visitor's overwrite in a full-mode KV or files
	// resource recorded the overwriter, so a later own would hand them
	// someone else's key. The first preset forgets those writers.
	if exists && old.Access == nil && old.WriteMode == "full" && old.Write != rules.WhoOwner {
		for _, table := range map[string]string{"kv": "site_storage_kv", "files": "site_storage_files"} {
			if _, e := repo.ClearResourceWriters(ctx, tx, table, call.SiteID, name); e != nil {
				return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
			}
		}
	}
	result, e := repo.UpsertResource(ctx, tx, call.SiteID, x)
	if e != nil {
		return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return ResourceResult{}, &Error{Code: "resource_kind_conflict", Message: "resource kind cannot change"}
	}
	if _, e = repo.DeleteResourceTables(ctx, tx, call.SiteID, name); e != nil {
		return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
	}
	for table, t := range x.Tables {
		if _, e = repo.InsertResourceTable(ctx, tx, call.SiteID, name, table, t); e != nil {
			return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
		}
	}
	if err = tx.Commit(); err != nil {
		return ResourceResult{}, &Error{Code: "internal_error", Message: "internal server error"}
	}

	unlock()
	if len(warnings) > 0 {
		sort.Strings(warnings)
	}
	return ResourceResult{Resource: x, Warnings: warnings, Created: !exists}, nil
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
