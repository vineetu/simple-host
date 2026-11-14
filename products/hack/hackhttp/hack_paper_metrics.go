package hackhttp

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	metrics "github.com/vsriram/simple-host/products/hack/papermetrics"
	"net/http"
	"strings"
)

// SitePaperRecorder resolves product identities for platform site requests.
type SitePaperRecorder struct{ metrics.RequestRecorder }

func (SitePaperRecorder) Site(r *http.Request, account, site string, user *model.User) {
	member := ""
	if user != nil {
		member = user.ID
		if user.Team != nil {
			member = user.Team.MemberID
		}
		if user.EventWebsite != nil {
			member = user.EventWebsite.OrganiserID
		}
	}
	p := r.URL.Path
	write := r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" &&
		(strings.Contains(p, "/storage/kv/") || strings.Contains(p, "/storage/sqlite/") || strings.Contains(p, "/storage/files/")) &&
		!strings.HasSuffix(p, "/query") && !strings.HasSuffix(p, "/download-link")
	(metrics.RequestRecorder{}).ResolveSite(r.Context(), account, site, member, write)
}

func (SitePaperRecorder) User(ctx context.Context, user *model.User) {
	if user == nil {
		return
	}
	member := user.ID
	if user.Team != nil {
		member = user.Team.MemberID
	}
	if user.EventWebsite != nil {
		member = user.EventWebsite.OrganiserID
	}
	(metrics.RequestRecorder{}).Member(ctx, member)
}
