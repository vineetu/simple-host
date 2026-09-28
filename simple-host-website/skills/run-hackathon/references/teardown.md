# Tearing down

Do both halves. The server alone is not enough.

## Warn first

Deleting the server destroys every entry and all saved data. There is no backup.
Before you begin, offer the organiser one archive of every entry
(`GET /v1/admin/export.tar.gz` with the admin key, or "Download all entries" on
the admin page): each site's files, saved data and lists. Participants can also
take their own copy.

## 1. Delete the server

The exact calls are per provider and are in
[providers.md](https://simple-host.app/v1/skills/run-hackathon/references/providers.md); each section ends with its teardown.

Two things are true almost everywhere and are the usual way a finished event
keeps costing money:

- **The disk often survives deleting the server.** UpCloud needs
  `?storages=1&backups=delete` on the DELETE; Oracle needs
  `--preserve-boot-volume false`. Deleting the server alone is not enough.
- **A reserved address can outlive the server too.** Check the provider's own
  list of floating or reserved IPs after teardown, not just its list of servers.

Hostinger is the exception worth knowing in advance: there is no published call
that deletes a VPS. You disable renewal on its subscription, so it stops at the
end of the paid term rather than immediately. Do not promise an organiser that
their Hostinger bill stops today.

## 2. Remove both DNS records

Delete `<event>` and `sites.<event>` at the registrar.

This is not tidiness. A record still pointing at a released cloud address means
whoever receives that address next is serving content under the organiser's
domain name, and can obtain a valid certificate for it.

## 3. Confirm

Ask the provider for its server list and confirm the event server is absent,
then check DNS:

```bash
dig +short <event>.<domain> A       # no longer the server's IP
dig +short sites.<event>.<domain> A # no longer the server's IP
```

Each returns nothing, or an address that is not the server's: a domain with a
wildcard or apex record answers every name, so after the records go the names
resolve to that instead (simple-hack.app does this).

Tell the organiser to check their provider's billing page in a day. A forgotten
disk or a floating IP is the usual way a finished event keeps costing money.
