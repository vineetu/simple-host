package accounts

import (
	"log"
	"strconv"
	"strings"
	"time"
)

type CodeSender interface {
	SendSignInCode(string, string, string) error
}
type NoticeSender interface {
	SendNotice(string, string, string) error
}

func mailSpan(d time.Duration) string {
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
func maskEmail(address string) string {
	local, domain, ok := strings.Cut(address, "@")
	if !ok || local == "" {
		return address
	}
	return local[:1] + "***@" + domain
}
func SendEmailChangeCode(mailer CodeSender, address, code string, ttl time.Duration) error {
	notice, ok := mailer.(NoticeSender)
	if !ok {
		return mailer.SendSignInCode(address, code, "")
	}
	return notice.SendNotice(address, "Simple Host confirmation code: "+code, `Someone asked to make this address the sign-in email of a Simple Host account.

Your confirmation code:

    `+code+`

It expires in `+mailSpan(ttl)+`. If you didn't ask for this, ignore this email: nothing changes.

Simple Host
`)
}

func SendEmailChangeCurrentCode(mailer CodeSender, current, newAddress, code string, ttl time.Duration, supportText func(string) string) error {
	notice, ok := mailer.(NoticeSender)
	if !ok {
		return mailer.SendSignInCode(current, code, "")
	}
	return notice.SendNotice(current, "Simple Host: confirm changing your sign-in email ("+code+")", supportText(`Someone signed in to your Simple Host account asked to change its sign-in email to `+maskEmail(newAddress)+`.

If that was you, enter this code together with the one sent to the new address:

    `+code+`

It expires in `+mailSpan(ttl)+`. If it wasn't you, do not share this code: without it nothing changes. Someone has one of your keys, so sign in and remove the keys you don't recognise, or write to support@simple-host.app.

Simple Host
`))
}

func EmailChangedNotice(newEmail, undoLink string, undoTTL time.Duration) string {
	text := "Your Simple Host sign-in email was changed to " + maskEmail(newEmail) + `. From now on you sign in with that address.

If this wasn't you, undo it (opens a page with an Undo button; works for ` + mailSpan(undoTTL) + `):
` + undoLink + `

Undoing puts the account back on this address, signs out every key and every sign-in on sites, and removes Google or GitHub sign-ins and connected apps added since the change. Questions: support@simple-host.app.

Simple Host
`
	return text
}

// NotifyEmailChanged schedules the old-inbox notice only when delivery is supported.
func NotifyEmailChanged(sender CodeSender, oldEmail, newEmail, undoLink string, undoTTL time.Duration, supportText func(string) string, run func(func())) {
	mailer, ok := sender.(NoticeSender)
	if !ok || !strings.Contains(oldEmail, "@") {
		return
	}
	text := EmailChangedNotice(newEmail, undoLink, undoTTL)
	run(func() {
		if err := mailer.SendNotice(oldEmail, "Your Simple Host sign-in email was changed", supportText(text)); err != nil {
			log.Printf("email change: notice to old address: %v", err)
		}
	})
}
