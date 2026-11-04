package storage

import "context"

import rules "github.com/vsriram/simple-host/internal/rules/storage"

// Call contains the resolved site, caller and policy facts for a storage operation.
type Call struct {
	Context                                 context.Context
	SiteID, OwnerID, SiteName, ResourceName string
	AccountID                               string
	Owner, OwnerOnSite, VisitorAI           bool
	VisitorID, LinkScope                    string
	Resource                                rules.Resource
	ActiveChecked                           bool
	CurrentMatrix                           rules.AccessMatrix
	Filter, SpamCheck                       string
	CheckedPost                             bool
	UploaderID, RuntimeDir                  string
	VisitorSessionID                        []byte
	VisitorSessionUserID                    string
	CSRFOK                                  bool
}
