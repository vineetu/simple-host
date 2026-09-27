# Email

Simple Host sends email through [Resend](https://resend.com): sign-in codes, sign-in alerts,
email-change confirmations and undo links, domain and idle-cleanup warnings, new-Submissions
digests, and the account-deleted confirmation. Without
`RESEND_API_KEY`, nothing is sent and email-code sign-in is off (Google sign-in and the admin
key still work).

`MAIL_FROM` must be on a domain you have verified with Resend.

<!-- settings:group=email -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `IDLE_REPLY_TO` | `support@simple-host.app` | email address | Reply-To address of the idle-cleanup emails. |
| `RESEND_API_KEY` | none | secret | A Resend API key for sending email (sign-in codes, alerts). Without it, email sign-in is off. **Security-sensitive.** |
| `MAIL_FROM` | `Simple Host <noreply@simple-host.app>` | text | The sender of every email, on a domain verified with Resend. |
<!-- /settings -->

## Recipes

**Email sign-in on a small box.** Verify your domain with Resend, then:

```
RESEND_API_KEY=re_...
MAIL_FROM=Simple Host <noreply@hack.example.com>
```
