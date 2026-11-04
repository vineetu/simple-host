package handler

import (
	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

func (h *SiteHandler) idleArchiveSvc(mailer replyNoticeSender) *sites.Service {
	return sites.New(h.database, h.disk, nil, 0, sites.Deps{
		LockSite: h.lockSite, DeletedRetention: h.limits.DeletedRetention,
		IdleArchive: sites.IdleArchiveDeps{
			Exempt: sites.IdleExempt{Handles: h.idleExempt.Handles, ReviewerEmail: h.idleExempt.ReviewerEmail, FamilySites: h.idleExempt.FamilySites},
			After:  h.idleAfter(), Grace: h.idleGrace(), Mailer: mailer, NewToken: newIdleToken,
			SiteAddress: func(s sites.IdleSite) string { return h.idleSiteAddress(db.IdleSite(s)) },
			OwnerApp:    func(s sites.IdleSite) string { return h.idleOwnerApp(db.IdleSite(s)) },
			Link:        h.idleLink, ReplyTo: h.idleReplyTo, Span: config.Span, Count: config.Count,
		},
	})
}
