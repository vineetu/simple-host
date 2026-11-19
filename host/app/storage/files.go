package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	siterepo "github.com/vsriram/simple-host/host/repository/sites"
	repository "github.com/vsriram/simple-host/host/repository/storage"
	mechanics "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// fileRulesTable is what file rules are written against, in the same words as
// key-value rules: one row per file, its path and its uploader (visitor_id,
// site_storage_files.writer_id, "" for the owner). It never becomes SQL.
var fileRulesTable = ruleengine.Table{Name: "files", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "path"}, {Name: "visitor_id"}}}

// fileRule decides op on a files resource with authorize, the decision
// key-value and SQLite use. The returned check answers for one stored file,
// or for the new file on an add; it is nil for the owner's own tools, which
// rules never limit.
func (s *Service) fileRule(c *Call, op ruleengine.Op) (fileCheck, *Error) {
	err := s.authorize(c.Context, c, "files", op)
	if err == nil && c.Owner {
		return nil, nil
	}
	if err == nil {
		if e := s.compileCallRules(c, fileRulesTable); e != nil {
			err = e.(*Error)
		}
	}
	if err != nil {
		// As refusals, so the routes report every refusal in order.
		return nil, &Error{Code: err.Code, Message: err.Message, Refusals: []*Error{err}}
	}
	plan, values, ai, visitor := c.rulePlan, c.ruleValues, c.VisitorAI, c.VisitorID
	return func(path, writer string) bool {
		row := ruleengine.Row{"path": ruleengine.Text(path), "visitor_id": ruleengine.Text(writer)}
		if op == ruleengine.OpAdd {
			ok, err := plan.EvalCheck(op, values, row)
			return err == nil && ok
		}
		// Edit and delete need read visibility too, and a visitor's AI changes
		// only its author's files (ruleAIConstraint).
		ok, err := plan.EvalUsing(ruleengine.OpRead, values, row)
		if op != ruleengine.OpRead && ok && err == nil {
			ok, err = plan.EvalUsing(op, values, row)
			ok = ok && (!ai || writer == visitor)
		}
		if op == ruleengine.OpEdit && ok && err == nil {
			ok, err = plan.EvalCheck(op, values, row)
		}
		return err == nil && ok
	}, nil
}

// fileCheck answers a file rule for one file and its uploader.
type fileCheck func(path, writer string) bool

// stored answers the check for a file in the bucket, reading its uploader. A
// nil check (the owner's own tools) allows every file.
func (check fileCheck) stored(ctx context.Context, s *Service, c Call, p string) (bool, error) {
	if check == nil {
		return true, nil
	}
	writer, err := repository.FileWriter(ctx, s.db, c.SiteID, c.ResourceName, p)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return check(p, writer), nil
}

// FilePutInput identifies a file and its caller.
type FilePutInput struct {
	Call Call
	Path string
	Body io.Reader
}

// FilePutResult describes the uploaded file.
type FilePutResult struct {
	Bytes       int
	ContentType string
}

// PutFile authorizes and publishes a file under the site write lock.
func (s *Service) PutFile(ctx context.Context, in FilePutInput) (FilePutResult, *Error, error) {
	call := in.Call
	if err := s.Gate(call, true); err != nil {
		return FilePutResult{}, err.(*Error), nil
	}
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return FilePutResult{}, nil, &Error{Code: "invalid_file", Message: "file too large; shrink photos to about 1600 px WebP"}
	}
	ctx, unlock, err := s.writeLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return FilePutResult{}, nil, err
	}
	defer unlock()
	dest := s.deps.FilePath(call, in.Path)
	var oldBytes int64
	existed := false
	if fi, e := os.Lstat(dest); e == nil {
		if !fi.Mode().IsRegular() {
			return FilePutResult{}, nil, &Error{Code: "file_exists", Message: "this path already exists"}
		}
		oldBytes, existed = fi.Size(), true
	} else if !errors.Is(e, os.ErrNotExist) {
		return FilePutResult{}, nil, e
	}
	call.Context = ctx
	op := ruleengine.OpAdd
	if existed {
		op = ruleengine.OpEdit
	}
	rule, auth := s.fileRule(&call, op)
	if auth != nil {
		// Someone who may add but not change gets file_exists, as on kv.
		add := in.Call
		add.Context = ctx
		if existed {
			if _, e := s.fileRule(&add, ruleengine.OpAdd); e == nil {
				return FilePutResult{}, nil, &Error{Code: "file_exists", Message: "this path already exists"}
			}
		}
		return FilePutResult{}, auth, nil
	}
	if rule != nil && !existed && !rule(in.Path, StampID(call.Owner, call.VisitorID)) {
		return FilePutResult{}, &Error{Code: "forbidden", Message: "add is not open to this visitor here"}, nil
	}
	// rowVisitorID is this write's SIM-360 push-fan-out target: the
	// existing file's own uploader when overwriting one (read under the
	// write lock, right here, before the overwrite -- the rule check on
	// the stored file needs this same lookup, so that result is reused
	// rather than queried twice), or the new file's own uploader (StampID
	// below) when adding one.
	rowVisitorID := ""
	if existed {
		writer, e := repository.FileWriter(ctx, s.db, call.SiteID, call.ResourceName, in.Path)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return FilePutResult{}, nil, e
		}
		if rule != nil && !rule(in.Path, writer) {
			return FilePutResult{}, nil, &Error{Code: "file_exists", Message: "this path already exists"}
		}
		rowVisitorID = writer
	}
	usage, err := s.MeasureSiteStorage(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return FilePutResult{}, nil, err
	}
	if !existed && usage.FileObjects[call.ResourceName] >= s.deps.FileObjectsLimit {
		return FilePutResult{}, nil, &Error{Code: "bucket_full", Message: "file bucket has too many objects; remove unused files"}
	}
	used := usage.Files
	if s.deps.CombinedPool {
		used = usage.Total()
	}
	if used-oldBytes+int64(len(body)) > s.FilesLimitBytes() {
		return FilePutResult{}, nil, &Error{Code: "site_full", Message: "file storage is full; shrink photos to about 1600 px WebP and remove unused files"}
	}
	f, err := mechanics.StageFile(dest, body)
	if err != nil {
		return FilePutResult{}, nil, err
	}
	defer os.Remove(f)
	if existed {
		err = repository.RecordFileEdit(ctx, s.db, call.SiteID, call.ResourceName, in.Path)
		if err == nil {
			err = os.Rename(f, dest)
		}
	} else if err = os.Link(f, dest); errors.Is(err, os.ErrExist) {
		return FilePutResult{}, nil, &Error{Code: "file_exists", Message: "this path already exists"}
	} else if err == nil {
		rowVisitorID = StampID(call.Owner, call.VisitorID)
		err = repository.RecordFileAdd(ctx, s.db, call.SiteID, call.ResourceName, in.Path, rowVisitorID)
		if err != nil {
			_ = os.Remove(dest)
		}
	}
	if err != nil {
		return FilePutResult{}, nil, err
	}
	s.deps.Committed(ctx)
	s.deps.ScanFile(call, in.Path, body)
	s.deps.MarkChanged()
	unlock()
	result := FilePutResult{Bytes: len(body), ContentType: mechanics.DetectContentType(body)}
	data := map[string]any{"path": in.Path, "size": result.Bytes, "content_type": result.ContentType, "owner_write": call.Owner}
	if rowVisitorID != "" {
		data["row_visitor_id"] = rowVisitorID
	}
	s.deps.Notify(call, "file.uploaded", data)
	return result, nil, nil
}

// FileLinkInput identifies the file for an owner download link.
type FileLinkInput struct {
	Call Call
	Path string
}

// FileLink contains the sealed token and its expiry.
type FileLink struct {
	Token   string
	Expires time.Time
}

// CreateFileLink authorizes an owner and creates a link for an existing path.
func (s *Service) CreateFileLink(ctx context.Context, in FileLinkInput) (FileLink, error) {
	if !in.Call.Owner {
		return FileLink{}, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	if _, err := os.Stat(s.deps.FilePath(in.Call, in.Path)); err != nil {
		return FileLink{}, &Error{Code: "file_not_found", Message: "file not found"}
	}
	expires := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	return FileLink{Token: s.deps.FileToken(in.Call, in.Path, expires), Expires: expires}, nil
}

// FileDeleteInput identifies a file and its caller.
type FileDeleteInput struct {
	Call Call
	Path string
}

// DeleteFile authorizes and removes a file under the shared write lock.
func (s *Service) DeleteFile(ctx context.Context, in FileDeleteInput) (*Error, error) {
	call := in.Call
	call.Context = ctx
	rule, auth := s.fileRule(&call, ruleengine.OpDelete)
	if auth != nil {
		return auth, nil
	}
	ctx, unlock, err := s.writeLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if rule != nil {
		if ok, err := rule.stored(ctx, s, call, in.Path); err != nil {
			return nil, err
		} else if !ok {
			return nil, &Error{Code: "file_not_found", Message: "file not found"}
		}
	}
	if err := os.Remove(s.deps.FilePath(call, in.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := repository.DeleteFile(ctx, s.db, call.SiteID, call.ResourceName, in.Path); err != nil {
		return nil, err
	}
	s.deps.Committed(ctx)
	s.deps.MarkChanged()
	unlock()
	return nil, nil
}

// FileReadInput identifies the file and whether a validated link grants access.
type FileReadInput struct {
	Call           Call
	Path           string
	LinkAuthorized bool
}

// OpenFile authorizes a file read and opens a regular file for streaming.
func (s *Service) OpenFile(ctx context.Context, in FileReadInput) (*os.File, os.FileInfo, *Error, error) {
	call := in.Call
	call.Context = ctx
	if !in.LinkAuthorized {
		rule, auth := s.fileRule(&call, ruleengine.OpRead)
		if auth != nil {
			return nil, nil, auth, nil
		}
		if ok, err := rule.stored(ctx, s, call, in.Path); err != nil {
			return nil, nil, nil, err
		} else if !ok {
			return nil, nil, nil, &Error{Code: "file_not_found", Message: "file not found"}
		}
	}
	f, err := os.Open(s.deps.FilePath(call, in.Path))
	if err != nil {
		return nil, nil, nil, &Error{Code: "file_not_found", Message: "file not found"}
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, nil, &Error{Code: "file_not_found", Message: "file not found"}
	}
	return f, fi, nil, nil
}

// FileListInput contains pagination and caller facts for listing files.
type FileListInput struct {
	Call          Call
	Prefix, After string
	Limit         int
	Valid         bool
	Root          string
}

// FileItem describes a visible file.
type FileItem struct {
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type"`
	VisitorID   string `json:"visitor_id,omitempty"`
	Mine        *bool  `json:"mine,omitempty"`
}

// ListFiles authorizes and lists files with their visible authors.
func (s *Service) ListFiles(ctx context.Context, in FileListInput) ([]FileItem, string, *Error, error) {
	call := in.Call
	call.Context = ctx
	var filter string
	rule, auth := s.fileRule(&call, ruleengine.OpRead)
	if auth != nil {
		return nil, "", auth, nil
	}
	if rule != nil {
		if call.rulePlan.ReadIsOwnOrOwner() && !call.OwnerOnSite {
			filter = call.VisitorID // the indexed lookup by uploader
		} else if call.rulePlan.Partial(ruleengine.OpRead, call.ruleValues) == ruleengine.True {
			rule = nil // every file is readable
		}
	}
	if !in.Valid {
		return nil, "", nil, &Error{Code: "invalid_limit", Message: "invalid limit"}
	}
	out := []FileItem{}
	next := ""
	if filter != "" {
		count := 0
		more := false
		e := repository.ForEachFilePath(ctx, s.db, call.SiteID, call.ResourceName, filter, in.After, in.Prefix, in.Limit+1, func(p string) (bool, error) {
			count++
			if count > in.Limit {
				more = true
				return false, nil
			}
			next = p
			if !mechanics.ValidObjectPath(p) || rule != nil && !rule(p, filter) {
				return true, nil
			}
			f, e := os.Open(s.deps.FilePath(call, p))
			if os.IsNotExist(e) {
				return true, nil
			}
			if e != nil {
				return false, e
			}
			fi, e := f.Stat()
			if e != nil || !fi.Mode().IsRegular() {
				f.Close()
				return true, nil
			}
			var b [512]byte
			n, _ := f.Read(b[:])
			f.Close()
			mine := true
			out = append(out, FileItem{Path: p, Bytes: fi.Size(), ContentType: mechanics.DetectContentType(b[:n]), Mine: &mine})
			return true, nil
		})
		if e != nil {
			return nil, "", nil, e
		}
		if !more {
			next = ""
		}
		return out, next, nil, nil
	}
	err := filepath.WalkDir(in.Root, func(fp string, d os.DirEntry, e error) error {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, e := filepath.Rel(in.Root, fp)
		if e != nil {
			return e
		}
		p := filepath.ToSlash(rel)
		if !mechanics.ValidObjectPath(p) || !strings.HasPrefix(p, in.Prefix) || p <= in.After {
			return nil
		}
		if rule != nil {
			if ok, e := rule.stored(ctx, s, call, p); e != nil || !ok {
				return e
			}
		}
		fi, e := d.Info()
		if e != nil {
			return e
		}
		f, e := os.Open(fp)
		if e != nil {
			return e
		}
		var b [512]byte
		n, _ := f.Read(b[:])
		f.Close()
		out = append(out, FileItem{Path: p, Bytes: fi.Size(), ContentType: mechanics.DetectContentType(b[:n])})
		if len(out) >= in.Limit+1 {
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, "", nil, err
	}
	if len(out) > in.Limit {
		next = out[in.Limit-1].Path
		out = out[:in.Limit]
	}
	if (call.Owner || call.OwnerOnSite || call.VisitorID != "") && len(out) > 0 {
		paths := make([]string, len(out))
		for i := range out {
			paths[i] = out[i].Path
		}
		writers, e := repository.FileWriters(ctx, s.db, call.SiteID, call.ResourceName, paths)
		if e != nil {
			return nil, "", nil, e
		}
		for i := range out {
			out[i].VisitorID = writers[out[i].Path]
			if !call.Owner && !call.OwnerOnSite {
				mine := out[i].VisitorID != "" && out[i].VisitorID == call.VisitorID
				out[i].VisitorID = ""
				out[i].Mine = &mine
			}
		}
	}
	return out, next, nil, nil
}

// DownloadFile validates a sealed link and opens its file for streaming.
func (s *Service) DownloadFile(ctx context.Context, token string) (*os.File, os.FileInfo, string, error) {
	invalid := func() (*os.File, os.FileInfo, string, error) {
		return nil, nil, "", &Error{Code: "file_link_invalid", Message: "link invalid"}
	}
	raw, err := s.deps.OpenFileToken(token)
	if err != nil {
		return invalid()
	}
	fields := strings.Split(string(raw), ".")
	if len(fields) != 5 && len(fields) != 6 {
		return invalid()
	}
	ownerID, siteID, name := fields[0], fields[1], fields[2]
	p, err := base64.RawURLEncoding.DecodeString(fields[3])
	expiry, ee := strconv.ParseInt(fields[4], 10, 64)
	if err != nil || ee != nil || time.Now().Unix() > expiry || expiry > time.Now().Add(10*time.Minute).Unix()+1 || !mechanics.ValidObjectPath(string(p)) {
		return invalid()
	}
	site, err := siterepo.GetSiteByID(ctx, s.db, siteID)
	if err != nil || site.Deleted || site.UserID != ownerID || site.OwnerSuspended || site.SiteSuspended {
		return invalid()
	}
	if len(fields) == 6 {
		claim, err := base64.RawURLEncoding.DecodeString(fields[5])
		if err != nil || !s.fileLinkScopeCurrent(ctx, site, string(claim)) {
			return invalid()
		}
	} else if s.deps.EventOwners {
		return invalid()
	}
	if !s.deps.FileResourceExists(ctx, siteID, name) {
		return invalid()
	}
	call := Call{SiteID: siteID, OwnerID: ownerID, SiteName: site.Name, ResourceName: name, Owner: true}
	f, fi, _, err := s.OpenFile(ctx, FileReadInput{Call: call, Path: string(p), LinkAuthorized: true})
	return f, fi, string(p), err
}

func (s *Service) fileLinkScopeCurrent(ctx context.Context, site model.Site, scope string) bool {
	if scope == "admin" {
		return true
	}
	kind, rest, ok := strings.Cut(scope, ":")
	if !ok {
		return false
	}
	id, memberID, ok := strings.Cut(rest, ":")
	if !ok || id == "" || memberID == "" {
		return false
	}
	return s.accounts.FileLinkScopeAllows(ctx, site, kind, id, memberID)
}
