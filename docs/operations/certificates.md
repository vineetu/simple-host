# Certificates on the hosted box

The root issuers under `deploy/site-certs/` and `deploy/domain-certs/` try
Let's Encrypt with their original arguments. Only an LE rate-limit failure
(`too many certificates`, `rateLimited` or the ACME rate-limit URN) enters the
ordered fallback: Google Trust Services, then ZeroSSL if Google fails for any
reason (including a rate limit, outage or missing credentials). Lock contention
stops the chain for a short retry, since changing CA cannot resolve it.

Platform sites on simple-host.app and simple-hack.app keep
DNS-01, the Vercel hooks, ECDSA, their wildcard names and lineage/deploy paths.
Custom domains keep HTTP-01, webroot and all requested names, including an
eligible www/bare partner. No nginx configuration changes are needed.
Address families still use operator-provided certificates and do not issue
via ACME.

The simple-host.site move is paused from 2026-10-09 (revisit about 2026-11-09).
Its issuer path and timer remain stopped and disabled. Existing certificates stay
on disk without revocation; the old TLS hosts only redirect to matching .app
addresses. See [Paused site base](site-base-paused.md).

## Configuration and credentials

`/etc/simple-host-cert-issuers.conf` is root-owned shell configuration, sourced
before the per-issuer configs. See `deploy/cert-issuers/config.example`.
`CERT_FALLBACK_CA=google,zerossl` is the new default. It accepts an ordered,
comma-separated list of `google` and `zerossl`; the old `zerossl` and `none`
values still work. `none` disables new fallbacks. An instance can override it
in its `.conf`; an exported value works when the shared config leaves it unset.
This is an issuer setting, not a Go app setting.

Google's endpoint is `https://dv.acme-v02.api.pki.goog/directory`; ZeroSSL's is
`https://acme.zerossl.com/v2/DV90`. Root-only (0600) credential files are:

- `GOOGLE_EAB_FILE=/etc/simple-host-secrets/google-eab.json`: `eab_kid`,
  `eab_hmac_key`, `server` (the Google endpoint above).
- `ZEROSSL_EAB_FILE=/etc/simple-host-secrets/zerossl-eab.json`: `success`,
  `eab_kid`, `eab_hmac_key`.

Register with support@simple-host.app. Google EAB expires if unused for seven
days and binds once to one account. The hosted account was registered on
2026-10-06; retain and reuse it. The helper selects the existing account from
Certbot's server-specific account directory and does not read EAB again when
it exists. EAB is used only for initial registration, in a 0600 temporary
config within a 0700 directory. Keys never enter argv. Configs and raw CA
diagnostics are private and removed on exit; failures report the CA without
printing its diagnostics. There must be one account per fallback endpoint.

## Queue pacing

`google-certbot` runs Certbot in its existing Python environment (the hosted
box uses `/opt/certbot-venv`, currently Certbot 5.8.0). It wraps the ACME
network send method only for the Google production endpoint. A shared file
lock and timestamps in `GOOGLE_ACME_STATE=/var/lib/simple-host-google-acme`
space all Google requests by at least two seconds, including challenges and
certificate polls (at most 30 requests/minute). Directory discovery identifies
new-order URLs; those are also spaced by 40 seconds (at most 90/hour).
HTTP 429 reserves the server's `Retry-After` cooldown for subsequent Google
requests. This state is shared across platform zones and custom domains on
this box. Other clients using the same Google project must share its quota.

Google's documented project quotas are 100 orders/hour, 100 challenge
requests/minute and 50 certificate polls/minute. The existing Certbot timer also uses this runner through
`certbot.service.d/zz-google-pacing.conf`, so Google renewal traffic shares
the same pacing. Other CAs pass through unchanged. Manually invoking plain
`certbot` bypasses both pacing and the shared lock; use
`/usr/local/lib/simple-host-cert-issuers/certbot-locked` followed by the
Certbot executable and arguments for operator operations. `google-certbot`
also takes the shared lock when called directly.

Platform queue bounds are now `BUDGET=10000` per rolling week, `DAILY=1000`
and `PER_RUN=30`; the old 40/week and 12/day caps would otherwise stop the
300-sign-up use case before fallback could run. These are local queue bounds,
not CA quotas. The app reads the same limits/issued-log format for estimates.
Requests remain oldest-first; a failed handle is marked and skipped until its
retry time, allowing later names through. Every issuance attempt is bounded to ten minutes after acquiring the lock. Existing custom-domain queue caps remain 50/day and 10/run.
There is still DNS/ACME latency and a queue; issuance is not instant under a
large simultaneous arrival. No burst simulation was run.

## Serialization, failures and recovery

All site issuers (.app, .site and .hack), custom-domain issuance/deletion,
Google/ZeroSSL fallback and `certbot.service` renewal share a waiting lock at
`/run/simple-host-certbot.lock`. `cert_run` in `runtime.sh` holds it only around
Certbot and its hooks. Queue locks remain per instance; DNS preparation and
Signal RPC do not hold the shared lock. `CERTBOT_LOCK_WAIT=180` bounds each
wait; timeout exits 75 and is transient. The paced Google runner inherits an
already-held lock when called by issuance or renewal. Address-family processing
never invokes Certbot; its certificates renew through the same locked service.

Failure markers now contain a readable first line (`transient: lock-busy`,
`transient: network-or-dns-timeout`, `ca: invalid-challenge`, etc.), `attempt`
and `retry_at` epoch seconds. Marker mtime is backdated so existing app
builds also infer the short deadline without a binary change. Transient retries start at 60 seconds, double to
120/240 and cap at 300 (`CERT_TRANSIENT_RETRY`, `CERT_TRANSIENT_MAX`). Issuer
timers run two minutes after the previous run finishes. Only explicit CA
refusals get `RETRY_AFTER=21600`; unknown/local failures retry soon. LE rate
limits still enter the ordered fallback chain. A transient domain failure does
not trigger a second immediate attempt without its www partner.

`requeue.py` migrates old markers automatically during normal issuer runs:
a recorded lock reason, or an old empty marker matching a journal-proven lock
failure, becomes immediately due. The matching failure must be within two
minutes of the marker's timestamp. A later CA refusal is preserved, and new
classified retries are never reset. There is no manual marker deletion or
special-case handle. Already-issued requests redeploy via the normal hook,
without another CA order. Requests remain queued until the ready marker exists.

## Renewal and alerts

Each successful lineage stores its own server and registered account, DNS
hooks or webroot, ECDSA setting and deploy hook. Renewals go to that CA through
the existing twice-daily `certbot.timer`; there is no global server override.
Changing the fallback knob, including `none`, does not change existing
renewals. Keep `/etc/letsencrypt/accounts` with renewal files in backups.
The helper restores the normal log directory in a newly issued lineage.
The renewal wrapper maps transient failures to exit 75; systemd retries after
two minutes, at most four starts per 15 minutes. Explicit CA refusals exit 1
and await the next normal renewal timer.

Signal reports the CA that issued the certificate, or a failed chain, through
the existing Hermes JSON-RPC. Account/recipient come from root-only
`/etc/sh-network-watch.env`. Locks and timestamps in
`CERT_ALERT_STATE=/var/lib/simple-host-cert-alerts` allow at most one note per
hour per domain (zone for platform wildcards). Alert failure does not undo
issuance. A later outcome within that hour is suppressed.

`simple-host-cert-watch.timer` checks independently every two minutes, even
when an issuer is stuck or exits before processing its queue. An outstanding
per-person request older than 15 minutes without a ready marker sends the
owner a Signal note through that same mechanism, throttled to one per
handle/base-domain/hour. It covers budgets, CA failures, DNS, lock waits and
deploy failures; no failure classification is required to alert. The request's
creation timestamp is the hand-off from site creation (and the app's safety
scan requeues any missed requests). Normal observation is within about 17
minutes of that request, excluding Signal outages or a down host.

## Install and verify

`sudo bash deploy/cert-issuers/install.sh` installs the shared fallback and
Google pacing/locking runners, retry/recovery helpers, issuer scripts,
DNS/deploy helpers, unchanged domain template, service/path/timer units,
the independent certificate watch and the locked Certbot renewal drop-in.
It enables the watch timer, refreshes active issuer timers and ensures each
enabled issuer path also has its two-minute retry timer (including Hack).
Wholly disabled issuer instances stay disabled. It preserves existing configs
and creates the shared config only when absent. `--google-default` explicitly
sets the hosted fallback chain and raises the platform queue caps in existing
instance configs. Every replaced file/config is backed up under
`/var/backups/simple-host-cert-issuers/<UTC timestamp>/`. No nginx configuration
is opened or edited. No app rebuild is needed for these script changes.

`make check` includes concurrent issuer fixtures with a Certbot internal-lock
stub, classification/backoff and bounded-wait checks, legacy journal migration,
missing-certificate alert/throttle checks, fallback/issuer fixtures and pacing
decision checks. ShellCheck covers the shell helpers. For real tests, set
`CERT_ISSUE_CA=google` after sourcing the helper; this explicitly skips LE.
Keep the queue scoped to throwaway names. Verify the actual HTTPS page and
served certificate, revoke/delete the test lineages, delete the throwaway
application account and DNS, and restore paused triggers. Retain the Google
ACME account for production reuse. Never contact LE production for tests.

Reference: [Google Public CA quotas](https://docs.cloud.google.com/certificate-manager/docs/quotas#public-ca-request-quotas),
[Google ACME account registration](https://docs.cloud.google.com/certificate-manager/docs/public-ca-tutorial),
[Certbot CA/config guide](https://eff-certbot.readthedocs.io/en/stable/using.html),
[ZeroSSL ACME](https://zerossl.com/documentation/acme).
The Public Suffix List entry remains planned for browser site boundaries and
Let's Encrypt's registered-domain accounting.

## Hosted smoke test, 2026-10-06

One throwaway account and its `probe` site were created through the normal API.
The app's wildcard request was copied into a scoped issuer queue; the issuer
used the normal DNS helper and deploy hook. A test-only Certbot wrapper returned
`rateLimited` for the primary without contacting Let's Encrypt and delegated
the fallback to real Certbot. Exactly one ZeroSSL wildcard was issued for
`*.zs-probe-8e353726-01.simple-host.app`. An HTTPS client validated the certificate,
its ZeroSSL ECC issuer and wildcard SAN, and received 200 with the expected page.
The renewal config retained ZeroSSL and its registered account, ECDSA and the
normal DNS/deploy hooks; an ordinary targeted `certbot renew` loaded it and
correctly skipped the fresh certificate without another issuance. A future
renewal was not forced (it would issue a second real certificate).

The account was deleted through the normal API (204); ZeroSSL revocation and
lineage deletion succeeded. Copied keys, ready/request markers and both test A
records were removed; paused issuer triggers were restored. The first ZeroSSL
attempt failed before registering an account or issuing a certificate, and the
hourly Signal throttle retained its failure note when the subsequent attempt
succeeded. No EAB values were found in retained plain Certbot logs. No Let's
Encrypt production request was made.

## Direct Google test, 2026-10-06

One throwaway application account (`gts-probe-0f9618fa-01`) hosted ten test sites.
Ten distinct real Google certificates were issued sequentially, each naming
`gNN.gts-probe-0f9618fa-01.simple-host.site` and its wildcard (NN = 01…10).
`CERT_ISSUE_CA=google` bypassed LE completely. The usual DNS hooks and ECDSA
were used. For this nested test naming, the normal deploy hook copied each
certificate in turn into the throwaway handle's existing nginx certificate
location via a temporary lineage alias; nginx configuration was not changed.

All ten returned 200 with the expected test page over verified HTTPS. A
separate TLS client confirmed the served certificate's fingerprint matched
the issued lineage, both SANs, and organization Google Trust Services
(intermediate WR1). Issuance took 33.1, 38.6, 39.4, 39.7, 39.2, 39.5, 39.3,
39.5, 39.4 and 39.5 seconds respectively (about 39 seconds each). Each renewal
file retained Google, the reused account, ECDSA and the DNS/deploy hooks.
A future renewal was not forced.

All ten certificates were revoked and deleted. The application account was
deleted through the normal API (204); its handle stays retired under the
normal deletion rules. DNS records, copied keys and test request/ready markers
were removed, and paused platform issuer triggers restored. The one Google
ACME account remains registered for production reuse, as required. No LE
production requests and no burst simulation were made.
