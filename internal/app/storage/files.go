package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	repository "github.com/vsriram/simple-host/internal/repository/storage"
	mechanics "github.com/vsriram/simple-host/internal/storage"
)

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
	ctx, unlock, err := s.WriteLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
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
	call.CurrentMatrix = call.Resource.Matrix()
	action := "add"
	if existed {
		action = "edit"
		if kvTaken(call) {
			return FilePutResult{}, nil, &Error{Code: "file_exists", Message: "this path already exists"}
		}
	}
	call.Context = ctx
	filter, err := s.Allow(&call, action, call.CurrentMatrix.Value(action))
	var auth *Error
	if err != nil {
		auth = err.(*Error)
		if !auth.Allowed {
			return FilePutResult{}, auth, nil
		}
	}
	if existed && filter != "" {
		writer, e := repository.FileWriter(ctx, s.db, call.SiteID, call.ResourceName, in.Path)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return FilePutResult{}, auth, e
		}
		if writer != filter {
			return FilePutResult{}, auth, &Error{Code: "file_exists", Message: "this path already exists"}
		}
	}
	usage, err := s.MeasureSiteStorage(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return FilePutResult{}, auth, err
	}
	if !existed && usage.FileObjects[call.ResourceName] >= s.deps.FileObjectsLimit {
		return FilePutResult{}, auth, &Error{Code: "bucket_full", Message: "file bucket has too many objects; remove unused files"}
	}
	used := usage.Files
	if s.deps.CombinedPool {
		used = usage.Total()
	}
	if used-oldBytes+int64(len(body)) > s.FilesLimitBytes() {
		return FilePutResult{}, auth, &Error{Code: "site_full", Message: "file storage is full; shrink photos to about 1600 px WebP and remove unused files"}
	}
	f, err := mechanics.StageFile(dest, body)
	if err != nil {
		return FilePutResult{}, auth, err
	}
	defer os.Remove(f)
	if existed {
		err = repository.RecordFileEdit(ctx, s.db, call.SiteID, call.ResourceName, in.Path)
		if err == nil {
			err = os.Rename(f, dest)
		}
	} else if err = os.Link(f, dest); errors.Is(err, os.ErrExist) {
		return FilePutResult{}, auth, &Error{Code: "file_exists", Message: "this path already exists"}
	} else if err == nil {
		err = repository.RecordFileAdd(ctx, s.db, call.SiteID, call.ResourceName, in.Path, StampID(call.Owner, call.VisitorID))
		if err != nil {
			_ = os.Remove(dest)
		}
	}
	if err != nil {
		return FilePutResult{}, auth, err
	}
	s.deps.Committed(ctx)
	s.deps.ScanFile(call, in.Path, body)
	s.deps.MarkChanged()
	unlock()
	result := FilePutResult{Bytes: len(body), ContentType: http.DetectContentType(body)}
	s.deps.Notify(call, "file.uploaded", map[string]any{"path": in.Path, "size": result.Bytes, "content_type": result.ContentType})
	return result, auth, nil
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
	filter, err := s.Access(&call, "delete")
	var auth *Error
	if err != nil {
		auth = err.(*Error)
		if !auth.Allowed {
			return auth, nil
		}
	}
	ctx, unlock, err := s.WriteLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return auth, err
	}
	defer unlock()
	if filter != "" {
		mine, err := repository.FileOwned(ctx, s.db, call.SiteID, call.ResourceName, in.Path, filter)
		if err != nil {
			return auth, err
		}
		if !mine {
			return auth, &Error{Code: "file_not_found", Message: "file not found"}
		}
	}
	if err := os.Remove(s.deps.FilePath(call, in.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return auth, err
	}
	if err := repository.DeleteFile(ctx, s.db, call.SiteID, call.ResourceName, in.Path); err != nil {
		return auth, err
	}
	s.deps.Committed(ctx)
	s.deps.MarkChanged()
	unlock()
	return auth, nil
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
	var auth *Error
	filter := call.Filter
	if !in.LinkAuthorized {
		var err error
		filter, err = s.Access(&call, "read")
		if err != nil {
			auth = err.(*Error)
			if !auth.Allowed {
				return nil, nil, auth, nil
			}
		}
	}
	if filter != "" {
		allowed, err := repository.FileOwned(ctx, s.db, call.SiteID, call.ResourceName, in.Path, filter)
		if err != nil {
			return nil, nil, auth, err
		}
		if !allowed {
			return nil, nil, auth, &Error{Code: "file_not_found", Message: "file not found"}
		}
	}
	f, err := os.Open(s.deps.FilePath(call, in.Path))
	if err != nil {
		return nil, nil, auth, &Error{Code: "file_not_found", Message: "file not found"}
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, auth, &Error{Code: "file_not_found", Message: "file not found"}
	}
	return f, fi, auth, nil
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
	filter, err := s.Access(&call, "read")
	var auth *Error
	if err != nil {
		auth = err.(*Error)
		if !auth.Allowed {
			return nil, "", auth, nil
		}
	}
	if !in.Valid {
		return nil, "", auth, &Error{Code: "invalid_limit", Message: "invalid limit"}
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
			if !mechanics.ValidObjectPath(p) {
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
			out = append(out, FileItem{Path: p, Bytes: fi.Size(), ContentType: http.DetectContentType(b[:n]), Mine: &mine})
			return true, nil
		})
		if e != nil {
			return nil, "", auth, e
		}
		if !more {
			next = ""
		}
		return out, next, auth, nil
	}
	err = filepath.WalkDir(in.Root, func(fp string, d os.DirEntry, e error) error {
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
		out = append(out, FileItem{Path: p, Bytes: fi.Size(), ContentType: http.DetectContentType(b[:n])})
		if len(out) >= in.Limit+1 {
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, "", auth, err
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
			return nil, "", auth, e
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
	return out, next, auth, nil
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
	site, err := db.GetSiteByID(ctx, s.db, siteID)
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

func (s *Service) fileLinkScopeCurrent(ctx context.Context, site db.Site, scope string) bool {
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
	switch kind {
	case "team":
		team, err := db.ResolveTeamIdentity(ctx, s.db, memberID, id)
		return err == nil && team.AccountID == site.UserID && team.TeamSlug == site.Name
	case "event":
		ev, err := db.GetEventByAccount(ctx, s.db, site.UserID)
		if err != nil || ev.ID != id || site.Name != ev.ID || ev.TakenDown() {
			return false
		}
		member, err := db.GetEventMember(ctx, s.db, ev.ID, memberID)
		return err == nil && member.Role == "organiser"
	}
	return false
}
