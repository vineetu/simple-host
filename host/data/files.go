package data

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

	accounts "github.com/vsriram/simple-host/host/access"
	repository "github.com/vsriram/simple-host/host/data/repo"
	model "github.com/vsriram/simple-host/host/model"
	siterepo "github.com/vsriram/simple-host/host/sites/repo"
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
	oldBytes, existed, err := storedFileSize(dest)
	if err != nil {
		return FilePutResult{}, nil, err
	}
	call.Context = ctx
	// rowVisitorID is this write's push-fan-out target: the stored
	// file's uploader on an overwrite, or the new file's own uploader.
	rowVisitorID, refusal, err := s.authorizeFilePut(&call, in.Path, existed)
	if refusal != nil || err != nil {
		return FilePutResult{}, refusal, err
	}
	if err = s.checkFileRoom(ctx, call, existed, oldBytes, int64(len(body))); err != nil {
		return FilePutResult{}, nil, err
	}
	if err = s.publishFile(ctx, call, dest, in.Path, body, existed, rowVisitorID); err != nil {
		return FilePutResult{}, nil, err
	}
	s.deps.Committed(ctx)
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

// storedFileSize reports the size of the regular file at dest, if one is
// there; any other kind of entry blocks the path.
func storedFileSize(dest string) (size int64, existed bool, err error) {
	fi, err := os.Lstat(dest)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return 0, false, nil
	case err != nil:
		return 0, false, err
	case !fi.Mode().IsRegular():
		return 0, false, &Error{Code: "file_exists", Message: "this path already exists"}
	}
	return fi.Size(), true, nil
}

// authorizeFilePut applies the file rules to an add or an edit and returns
// the file's uploader: the new file's own, or the stored file's (read under
// the write lock, before the overwrite). Someone who may add but not change
// gets file_exists, as on kv.
func (s *Service) authorizeFilePut(c *Call, path string, existed bool) (writer string, refusal *Error, err error) {
	add := *c
	op := ruleengine.OpAdd
	if existed {
		op = ruleengine.OpEdit
	}
	rule, auth := s.fileRule(c, op)
	exists := &Error{Code: "file_exists", Message: "this path already exists"}
	if auth != nil && existed {
		if _, e := s.fileRule(&add, ruleengine.OpAdd); e == nil {
			return "", nil, exists
		}
	}
	if auth != nil {
		return "", auth, nil
	}
	if !existed {
		writer = StampID(c.Owner, c.VisitorID)
		if rule != nil && !rule(path, writer) {
			return "", &Error{Code: "forbidden", Message: "add is not open to this visitor here"}, nil
		}
		return writer, nil, nil
	}
	writer, err = repository.FileWriter(c.Context, s.db, c.SiteID, c.ResourceName, path)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	if rule != nil && !rule(path, writer) {
		return "", nil, exists
	}
	return writer, nil, nil
}

// checkFileRoom refuses a write that the bucket's object limit or the site's
// file storage cannot take.
func (s *Service) checkFileRoom(ctx context.Context, call Call, existed bool, oldBytes, newBytes int64) error {
	usage, err := s.MeasureSiteStorage(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return err
	}
	if !existed && usage.FileObjects[call.ResourceName] >= s.deps.FileObjectsLimit {
		return &Error{Code: "bucket_full", Message: "file bucket has too many objects; remove unused files"}
	}
	used := usage.Files
	if s.deps.CombinedPool {
		used = usage.Total()
	}
	if used-oldBytes+newBytes > s.FilesLimitBytes() {
		return &Error{Code: "site_full", Message: "file storage is full; shrink photos to about 1600 px WebP and remove unused files"}
	}
	return nil
}

// publishFile stages body and moves it to dest, recording the edit, or the
// new file and its uploader.
func (s *Service) publishFile(ctx context.Context, call Call, dest, path string, body []byte, existed bool, writer string) error {
	staged, err := mechanics.StageFile(dest, body)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if existed {
		if err = repository.RecordFileEdit(ctx, s.db, call.SiteID, call.ResourceName, path); err != nil {
			return err
		}
		return os.Rename(staged, dest)
	}
	if err = os.Link(staged, dest); errors.Is(err, os.ErrExist) {
		return &Error{Code: "file_exists", Message: "this path already exists"}
	} else if err != nil {
		return err
	}
	if err = repository.RecordFileAdd(ctx, s.db, call.SiteID, call.ResourceName, path, writer); err != nil {
		_ = os.Remove(dest)
	}
	return err
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
	rule, auth := s.fileRule(&call, ruleengine.OpRead)
	if auth != nil {
		return nil, "", auth, nil
	}
	uploader := ""
	if rule != nil && call.rulePlan.ReadIsOwnOrOwner() && !call.OwnerOnSite {
		uploader = call.VisitorID // the indexed lookup by uploader
	} else if rule != nil && call.rulePlan.Partial(ruleengine.OpRead, call.ruleValues) == ruleengine.True {
		rule = nil // every file is readable
	}
	if !in.Valid {
		return nil, "", nil, &Error{Code: "invalid_limit", Message: "invalid limit"}
	}
	var out []FileItem
	var next string
	var err error
	if uploader != "" {
		out, next, err = s.listUploaderFiles(ctx, call, in, rule, uploader)
	} else {
		out, next, err = s.listBucketFiles(ctx, call, in, rule)
	}
	if err != nil {
		return nil, "", nil, err
	}
	return out, next, nil, nil
}

// listUploaderFiles pages through the files one visitor uploaded, by the
// uploader index. The cursor is the last path visited, listed or not.
func (s *Service) listUploaderFiles(ctx context.Context, call Call, in FileListInput, rule fileCheck, uploader string) ([]FileItem, string, error) {
	out, next, visited := []FileItem{}, "", 0
	err := repository.ForEachFilePath(ctx, s.db, call.SiteID, call.ResourceName, uploader, in.After, in.Prefix, in.Limit+1, func(p string) (bool, error) {
		if visited++; visited > in.Limit {
			return false, nil
		}
		next = p
		if !mechanics.ValidObjectPath(p) || rule != nil && !rule(p, uploader) {
			return true, nil
		}
		item, ok, err := regularFileItem(s.deps.FilePath(call, p), p)
		if ok {
			mine := true
			item.Mine = &mine
			out = append(out, item)
		}
		return err == nil, err
	})
	if err != nil {
		return nil, "", err
	}
	if visited <= in.Limit {
		next = ""
	}
	return out, next, nil
}

// listBucketFiles walks the bucket in path order for the files the caller may
// read, then shows who uploaded them.
func (s *Service) listBucketFiles(ctx context.Context, call Call, in FileListInput, rule fileCheck) ([]FileItem, string, error) {
	out := []FileItem{}
	err := filepath.WalkDir(in.Root, func(fp string, d os.DirEntry, err error) error {
		hidden := err == nil && strings.HasPrefix(d.Name(), ".")
		switch {
		case errors.Is(err, os.ErrNotExist):
			return nil
		case err != nil:
			return err
		case hidden && d.IsDir():
			return filepath.SkipDir
		case hidden || !d.Type().IsRegular():
			return nil
		}
		rel, err := filepath.Rel(in.Root, fp)
		if err != nil {
			return err
		}
		p := filepath.ToSlash(rel)
		if !mechanics.ValidObjectPath(p) || !strings.HasPrefix(p, in.Prefix) || p <= in.After {
			return nil
		}
		if ok, err := rule.stored(ctx, s, call, p); err != nil || !ok {
			return err
		}
		item, ok, err := regularFileItem(fp, p)
		if ok {
			out = append(out, item)
		}
		if err == nil && len(out) > in.Limit {
			return io.EOF
		}
		return err
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, "", err
	}
	next := ""
	if len(out) > in.Limit {
		next = out[in.Limit-1].Path
		out = out[:in.Limit]
	}
	return out, next, s.showFileUploaders(ctx, call, out)
}

// regularFileItem describes the regular file at fp, sniffing its content
// type; a missing or non-regular file is skipped.
func regularFileItem(fp, p string) (FileItem, bool, error) {
	f, err := os.Open(fp)
	if os.IsNotExist(err) {
		return FileItem{}, false, nil
	}
	if err != nil {
		return FileItem{}, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return FileItem{}, false, nil
	}
	var b [512]byte
	n, _ := f.Read(b[:])
	return FileItem{Path: p, Bytes: fi.Size(), ContentType: mechanics.DetectContentType(b[:n])}, true, nil
}

// showFileUploaders tells a signed-in caller who uploaded each listed file:
// the owner sees the uploader, a visitor only whether it was them.
func (s *Service) showFileUploaders(ctx context.Context, call Call, out []FileItem) error {
	if len(out) == 0 || !call.Owner && !call.OwnerOnSite && call.VisitorID == "" {
		return nil
	}
	paths := make([]string, len(out))
	for i := range out {
		paths[i] = out[i].Path
	}
	writers, err := repository.FileWriters(ctx, s.db, call.SiteID, call.ResourceName, paths)
	if err != nil {
		return err
	}
	for i := range out {
		writer := writers[out[i].Path]
		if call.Owner || call.OwnerOnSite {
			out[i].VisitorID = writer
			continue
		}
		mine := writer != "" && writer == call.VisitorID
		out[i].Mine = &mine
	}
	return nil
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
	}
	if !s.deps.FileResourceExists(ctx, siteID, name) {
		return invalid()
	}
	call := Call{SiteID: siteID, OwnerID: ownerID, SiteName: site.Name, ResourceName: name, Owner: true}
	f, fi, _, err := s.OpenFile(ctx, FileReadInput{Call: call, Path: string(p), LinkAuthorized: true})
	return f, fi, string(p), err
}

// fileLinkScopeCurrent: a link made for a member or editor ("as:<person>")
// works only while that person may still act as the site's owner.
func (s *Service) fileLinkScopeCurrent(ctx context.Context, site model.Site, scope string) bool {
	personID, ok := strings.CutPrefix(scope, "as:")
	if !ok {
		return false
	}
	allowed, err := accounts.ActsAsOwner(ctx, s.db, personID, site.UserID, site.Name)
	return err == nil && allowed
}
