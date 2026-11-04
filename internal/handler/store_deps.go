package handler

import (
	"strconv"
	"time"

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/app/notifications"
)

func (h *SiteHandler) storeAccounts() *accountapp.Service { return accountapp.New(h.database) }

func (h *SiteHandler) storeMail() *notifications.StoreMail {
	return &notifications.StoreMail{Database: h.database, Mailer: h.mailer, EachMinutes: h.savedData.NotifyEachMinutes, DailyHours: h.savedData.NotifyDailyHours, ExportBase: h.exportLinkBase(), MainSiteURL: h.mainSiteURL(), StopToken: h.notifyStopToken, MailError: notificationMailError}
}

const legacyDeclareFirst = "declare_first"

// storeSpan formats durations for the legacy HTTP messages.
func storeSpan(d time.Duration) string {
	n, unit := int(d/time.Second), "second"
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		n, unit = int(d/(24*time.Hour)), "day"
	case d >= time.Hour && d%time.Hour == 0:
		n, unit = int(d/time.Hour), "hour"
	case d >= time.Minute && d%time.Minute == 0:
		n, unit = int(d/time.Minute), "minute"
	}
	if n == 1 {
		return "one " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
