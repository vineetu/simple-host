package deploy

import (
	"bytes"
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

// The deploy workflow behind POST/PUT /v1/sites/{name} (an archive body) and
// POST/PUT /v1/sites/{name}/files (a JSON body of files). A route reads the
// request in two steps because the body is read under a limit only Sites
// knows: AdmitDeploy checks the target and options and says how large the
// upload may be, the route reads the body under that limit, and Deploy
// prepares the files and creates or updates the site.

// DeployTarget is a deploy as its route names it, before the body is read.
type DeployTarget struct {
	User *model.User
	Name string
	// New is the create route (POST). Otherwise it is an update (PUT), which
	// creates the site when it is missing only with ?create=1.
	New bool
	// PublishParam and CreateParam are the raw ?publish= and ?create= values.
	PublishParam, CreateParam string
}

// Admission is what AdmitDeploy allowed: whether the version goes live,
// whether a missing site is created, and the limit the body is read under.
type Admission struct {
	Publish, Create bool
	Limit           int64
	Override        bool
}

// AdmitDeploy checks a deploy's target and options before its body is read.
// A new site's name is held to the reserved-name list; an update's never is,
// so an existing site is always re-deployable. Refusals are *Error with codes
// invalid_name, name_reserved, invalid_request, site_quota_reached and
// no_personal_sites.
func (s Service) AdmitDeploy(ctx context.Context, t DeployTarget) (Admission, error) {
	var adm Admission
	if t.New {
		if err := s.CreatePreflight(ctx, PreflightInput{User: t.User, Name: t.Name, CheckName: true, Publish: t.PublishParam}); err != nil {
			return adm, err
		}
		adm.Publish = true // a new site's first version always goes live
	} else {
		pre, err := s.UpdatePreflight(ctx, UpdatePreflightInput{User: t.User, Name: t.Name, Publish: t.PublishParam, Create: t.CreateParam})
		if err != nil {
			return adm, err
		}
		adm.Publish, adm.Create = pre.Publish, pre.Create
	}
	adm.Limit, adm.Override = s.Accounts.SiteLimit(ctx, t.User, s.Deps.Limits, sites.Upload.Body)
	return adm, nil
}

// DeployInput is an admitted deploy with the body its route read: an
// archive, or JSON files (with keep=1, a patch onto the live files).
type DeployInput struct {
	DeployTarget
	Admission
	// Access and Viewers are the access chosen at creation.
	Access  string
	Viewers []string
	Author  string
	// Archive is the archive body; ContentDigest its optional X-Content-Digest.
	Archive       []byte
	ContentDigest string
	// Files are the JSON body's files; with Keep they patch the live files
	// and Remove names live files to drop.
	Files  map[string][]byte
	Keep   bool
	Remove []string
	// IfMatch is the raw If-Match header of an update.
	IfMatch string
	// ExpectedID, when CheckID is set, is the site an operator acting as the
	// owner named; another site of that name is not found.
	ExpectedID string
	CheckID    bool
}

// DeployResult is the site a deploy left.
type DeployResult struct {
	Site model.Site
	// Created: the deploy created the site (201).
	Created bool
	// Published: the new version is live. An update stored with
	// publish=false is not, and Version is the version it stored.
	Published       bool
	Version         int
	PreviousVersion int
	// FileBytes is the site's footprint on disk after the deploy.
	FileBytes int64
}

// Deploy prepares an admitted deploy's files and creates or updates the site
// under the deploy locks. Refusals: *UploadError (the files), *GateError (a
// product's write gate) and *Error, as Create and Update return them, plus
// invalid_if_match.
func (s Service) Deploy(ctx context.Context, in DeployInput) (DeployResult, error) {
	files, digest, err := s.prepareDeploy(in)
	if err != nil {
		return DeployResult{}, err
	}
	if in.New {
		created, err := s.Create(ctx, CreateInput{Access: in.Access, Viewers: in.Viewers, User: in.User, Name: in.Name, ArchiveSHA: digest, Author: in.Author, Files: files})
		return DeployResult{Site: created.Site, Created: true, Published: true, FileBytes: created.FileBytes}, err
	}
	ifMatch, err := sites.ParseIfMatch(in.IfMatch, s.Deps.PersonalSitesEnabled)
	if err != nil {
		return DeployResult{}, err
	}
	update := UpdateInput{
		Access: in.Access, Viewers: in.Viewers, User: in.User, Name: in.Name, ArchiveSHA: digest, Author: in.Author, Files: files,
		Publish: in.Publish, CreateIfMissing: in.Create, ExpectedID: in.ExpectedID, CheckID: in.CheckID, IfMatch: ifMatch,
	}
	if in.Keep {
		update.Keep, update.Remove, update.LimitOverride, update.Limit = true, in.Remove, in.Override, in.Limit
	}
	result, err := s.Update(ctx, update)
	if result.Created != nil {
		return DeployResult{Site: result.Created.Site, Created: true, Published: true, FileBytes: result.Created.FileBytes}, err
	}
	return DeployResult{Site: result.Site, Published: result.Published, Version: result.Version, PreviousVersion: result.PreviousVersion, FileBytes: result.FileBytes}, err
}

// prepareDeploy applies the path, secret, size and file-type guards to the
// upload and returns its files and digest. A keep=1 patch is checked on its
// own here and again, merged, under the deploy locks; it may be empty.
func (s Service) prepareDeploy(in DeployInput) (map[string][]byte, string, error) {
	switch {
	case in.Archive != nil:
		return s.PrepareArchive(ArchivePreparationInput{Body: in.Archive, Filename: archiveFilename(in.Name, in.Archive), ContentDigest: in.ContentDigest, Limit: in.Limit, Override: in.Override})
	case in.Keep && len(in.Files) == 0:
		return in.Files, "", nil
	case in.Keep:
		clean, _, err := s.PrepareFiles(FilePreparationInput{Files: in.Files, Limit: in.Limit, Override: in.Override, Patch: true})
		return clean, "", err
	}
	return s.PrepareFiles(FilePreparationInput{Files: in.Files, Limit: in.Limit, Override: in.Override})
}

// archiveFilename names the archive by its contents (a zip or a tar.gz).
func archiveFilename(siteName string, body []byte) string {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("PK\x03\x04")) {
		return siteName + ".zip"
	}
	return siteName + ".tar.gz"
}
