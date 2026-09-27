# Rate limits

Each rate limit is `<burst>,<every>`: that many requests at once, then one more every `<every>`
(a duration: `500ms`, `5s`, `6m`, `1h`). `RATE_LIMIT_SIGNIN_IP=20,5s` allows 20 sign-in requests
from one address at once, then one every 5 seconds. Limits are per client address unless the
description says otherwise.

**Security-sensitive** limits (sign-in, visitor sign-in, the connector's OAuth) can be made
stricter freely but at most four times looser than the default; anything looser stops the server
at startup. The others may be set to anything, with a startup warning past ten times looser.

<!-- settings:type=rate -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `RATE_LIMIT_SIGNIN_IP` | `20,5s` | stricter freely; loosest `80,1.25s` | Sign-in and email-change requests per address. **Security-sensitive.** |
| `RATE_LIMIT_SIGNIN_EMAIL` | `5,50s` | stricter freely; loosest `20,12.5s` | Sign-in codes sent to one email address. **Security-sensitive.** |
| `RATE_LIMIT_VISITOR_OAUTH` | `20,5s` | stricter freely; loosest `80,1.25s` | Visitor Google sign-ins per address. **Security-sensitive.** |
| `RATE_LIMIT_VISITOR_AUTH` | `20,5s` | stricter freely; loosest `80,1.25s` | Visitor email-code sign-ins per address. **Security-sensitive.** |
| `RATE_LIMIT_VISITOR` | `20,5s` | stricter freely; loosest `80,1.25s` | Finishing a visitor sign-in and signing out, per address. **Security-sensitive.** |
| `RATE_LIMIT_OAUTH_REGISTER` | `10,6m` | stricter freely; loosest `40,1m30s` | AI app registrations on the connector, per address. **Security-sensitive.** |
| `RATE_LIMIT_OAUTH_AUTHORIZE` | `30,2s` | stricter freely; loosest `120,500ms` | AI app sign-in requests on the connector, per address. **Security-sensitive.** |
| `RATE_LIMIT_OAUTH_TOKEN` | `30,2s` | stricter freely; loosest `120,500ms` | AI app token requests on the connector, per address. **Security-sensitive.** |
| `RATE_LIMIT_UPLOAD` | `30,10s` | any (warns past 10× looser) | Uploads and deploys per client. |
| `RATE_LIMIT_SITE_OPS` | `30,2s` | any (warns past 10× looser) | Deleting, changing and restoring sites, per address. |
| `RATE_LIMIT_EXPORT` | `10,10s` | any (warns past 10× looser) | Site downloads per address. |
| `RATE_LIMIT_STATE` | `60,1s` | any (warns past 10× looser) | Saved-data and list writes per client. |
| `RATE_LIMIT_DOMAIN_CHECK` | `10,10s` | any (warns past 10× looser) | "Check again" on a domain, per address. |
| `RATE_LIMIT_DOMAIN_CHECK_USER` | `3,30s` | any (warns past 10× looser) | "Check again" on a domain, per account. |
| `RATE_LIMIT_AI_IP` | `20,12s` | any (warns past 10× looser) | AI create requests per address. |
| `RATE_LIMIT_AI_USER` | `30,10s` | any (warns past 10× looser) | AI create requests per account. |
| `RATE_LIMIT_TRANSCRIBE` | `60,3s` | any (warns past 10× looser) | Voice input requests, per address and per account. |
<!-- /settings -->

## Recipes

**A busy event on one box** (many people behind one venue address):

```
RATE_LIMIT_SIGNIN_IP=80,1250ms
RATE_LIMIT_UPLOAD=120,2s
RATE_LIMIT_STATE=240,250ms
```
