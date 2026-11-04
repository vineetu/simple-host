package handler

import (
	"github.com/vsriram/simple-host/internal/app/sites"
)

func (h *SiteHandler) idleArchiveSvc(mailer replyNoticeSender) *sites.Service {
	return sites.New(h.database, h.disk, nil, 0, sites.Deps{
		LockSite: h.lockSite, DeletedRetention: h.limits.DeletedRetention,
		IdleArchive: sites.IdleArchiveDeps{
			Exempt: sites.IdleExempt{Handles: h.idleExempt.Handles, ReviewerEmail: h.idleExempt.ReviewerEmail, FamilySites: h.idleExempt.FamilySites},
			After:  h.idleAfter(), Grace: h.idleGrace(), Mailer: mailer, NewToken: newIdleToken,
			SiteAddress: func(s sites.IdleSite) string { return h.idleSiteAddress(s) },
			OwnerApp:    func(s sites.IdleSite) string { return h.idleOwnerApp(s) },
			Link:        h.idleLink, ReplyTo: h.idleReplyTo, Span: limitSpan, Count: limitCount,
		},
	})
}
