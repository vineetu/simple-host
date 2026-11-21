package sites

import (
	"context"
	"net/http"

	"github.com/vsriram/simple-host/host/model"
)

// GroupRef names a holding account's group.
type GroupRef struct{ ID, Slug, AccountID string }

// Group is the address and website state of a holding account's group.
type Group struct {
	GroupRef
	CustomWebsite bool
	TakenDown     bool
}

// GroupHosts answers for groups and their addresses. Nil on a platform-only install.
type GroupHosts interface {
	// ByAccount returns sql.ErrNoRows when the account holds no group.
	ByAccount(context.Context, string) (Group, error)
	// BySlug returns sql.ErrNoRows when no group has this address label.
	BySlug(context.Context, string) (Group, error)
	HasSite(ctx context.Context, groupID, siteName string) (bool, error)
	PinnedVersion(ctx context.Context, accountID, siteName string) (version int, ok bool, err error)
	// ServePage runs only for GET/HEAD, after the platform's custom website branch.
	ServePage(http.ResponseWriter, *http.Request, model.User) bool
	Texts() GroupTexts
}

type GroupTexts struct {
	NotFoundTitle, NotFoundBody string
	NoPersonalSites             string
}
