---
name: run-hackathon
description: Stand up a private Simple Host instance for a hackathon on the organiser's own cloud account, create participant accounts in bulk, hand out their keys, and tear the whole thing down afterwards. Use when someone wants to run a hackathon, a class project showcase, an internal build day or any event where non-technical people need somewhere to publish what they make. Drives provider → server → DNS → install → accounts → teardown. The organiser never opens a terminal; this skill does.
---

# Run a hackathon

The organiser gets a private instance on **their own cloud account**, paid for by
them, that you create and later destroy. Participants get an API key each and
publish with their own coding agent. Nobody signs in.

**Their credentials never leave their machine.** You use them here and do not
transmit them anywhere.

## What the organiser needs before you start

1. **A cloud account with API access.** UpCloud is the tested path. Ask them to
   create an API token in their provider's console and paste it to you.
2. **A domain, or not.** Either works:
   - **No domain**: they get two free hostnames under a domain we run. You claim
     them; the organiser does nothing. This is the default, and the simplest.
   - **Their own domain**: they add two DNS records themselves. Choose this if
     they want their own name on it, or if they want email or Google sign-in
     later, which need records only they can add.
3. **Nothing else.** No email provider, no Google project, no Docker, no terminal.

Tell them the cost before creating anything: about five dollars a month, billed
hourly, so a weekend event is cents. Confirm before you create the server.

## The flow

### 1. Pick the two hostnames

An event needs **two**, always:

- `<event>.<their-domain>` — dashboard, sign-in and API
- `sites.<event>.<their-domain>` — every participant's content

They are separate on purpose. A participant's page must never share an origin
with the admin interface, or anything published could script the dashboard of
whoever is viewing it.

### 2. Create the server

Smallest Ubuntu 24.04 plan with at least 1 GB of memory.
`references/providers.md` has the exact calls per provider.

**Do not install a provider CLI.** Every provider except Oracle is driven with a
bearer token and `curl`, which is already on the organiser's machine. An install
on somebody else's laptop is a version, a login and a new way to fail.

Generate an SSH key locally and pass the public half at creation, so nothing
needs a password. Never overwrite a key that already exists.

### 3. Point DNS at it

Two A records, both to the server's public IPv4 address.

**If they have no domain**, claim free hostnames with the organiser's own
Simple Host account key:

```
POST https://simple-host.app/v1/events
{"name": "stanford-cs-2026", "ip": "<server IPv4>", "domain": "simple-host.app"}
```

Always send `domain` explicitly and remember which one you used, because release
needs it too. If the claim is refused because the domain is at its weekly
certificate limit, retry with `"domain": "agent-deploy.dev"`. If a name is
already in use, ask for a different one rather than guessing.

The response gives `host` and `content_host`. Names are lowercased, must be 1 to
40 letters, digits or hyphens, and a name someone else holds answers 409, so ask
for another. A claim expires after three weeks and re-claiming the same name
extends it.

**If they have their own domain**, they add the two records at their registrar.
See `references/dns.md`.

Either way, wait until both names resolve before continuing. Installing first
works, but certificate issuance fails and the organiser sees browser warnings,
which is far more alarming than waiting.

### 4. Install

One command over SSH, from `references/install.md`. It is idempotent: if it
fails halfway, run it again. It installs Docker, pulls the published image,
starts the stack and prints a JSON summary.

**The summary contains the admin key and it is shown exactly once.** Give it to
the organiser immediately and tell them to keep it. Nothing else can display it,
and without it they are not the administrator of their own instance.

### 5. Create participant accounts

With the admin key, from their roster:

```
POST https://<event-host>/v1/admin/users
{"emails": ["ada@example.edu", "alan@example.edu"]}
```

or, for a walk-up event where you do not have names yet:

```
{"count": 30, "prefix": "team"}
```

Up to 200 per request. The response gives each participant a username, a handle
and an **api_key**. Existing accounts are skipped and their keys are never
re-disclosed, so re-running is safe but will not recover a lost key.

Offer the organiser the list as CSV so they can paste it into a spreadsheet or a
mail merge.

### 6. Tell participants what to do

Each participant needs two things: their key, and one instruction.

```
Read https://<event-host>/llms.txt and follow it exactly.
My Simple Host API key is: <their key>
Ask me what I want to build, then build it and publish it.
```

That works in any agent that can fetch a URL. The instance's own `llms.txt`
names the event's hostnames, not simple-host.app, so their site lands on the
organiser's server.

### 7. Collect the entries

Every entry is a link of the form
`https://sites.<event>.<their-domain>/<handle>/<project>/`.

There is no organiser screen listing them. Ask each team for their final link.
One account can hold several sites, so "their final link" is a real question,
not a formality.

### 8. Tear it down

When the event ends, **delete the server and remove both DNS records**. See
`references/teardown.md`.

Do not skip the DNS records. A record left pointing at a released cloud address
means whoever receives that address next is serving content under that domain
name.

If you claimed free hostnames, release them:

```
DELETE https://simple-host.app/v1/events/<name>?domain=<the domain you claimed>
```

The `domain` must match the claim. Omitting it targets the default domain, which
answers 404 and leaves the real records in place.

If the organiser used their own domain, they delete the two records themselves.

Warn them first: deleting the server destroys every entry. If anyone wants to
keep what they built, they take a copy before you start.

## What this does not do

Say these plainly if asked, rather than working around them:

- **No organiser view of entries.** You keep the list of links.
- **No sign-in for participants.** Keys only. Email codes and Google are
  possible on an instance whose domain the organiser controls, but they are
  configured separately and are not part of this flow.
- **Saved data is open.** Anyone who can load a page can change that site's
  stored data. Files change only with the owner's key. If saved data matters for
  judging, have teams copy it before judging starts.
- **No backups.** A disk failure during the event loses the event.
- **Free hostnames expire.** A claim lasts three weeks. Re-claim the same name to
  extend it. After that a sweep removes the records, so a long-running instance
  should use the organiser's own domain.
