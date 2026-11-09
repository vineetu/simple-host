# Email

Simple Host sends email through [Resend](https://resend.com): sign-in codes, sign-in alerts,
email-change confirmations and undo links, domain and idle-cleanup warnings, new-Submissions
digests, and the account-deleted confirmation. Without
`RESEND_API_KEY`, nothing is sent and email-code sign-in is off (Google sign-in and the admin
key still work).

`MAIL_FROM` must be on a domain you have verified with Resend.


## Recipes

**Email sign-in on a small box.** Verify your domain with Resend, then:
