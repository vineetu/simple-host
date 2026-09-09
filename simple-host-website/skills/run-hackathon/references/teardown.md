# Tearing down

Do both halves. The server alone is not enough.

## Warn first

Deleting the server destroys every entry and all saved data. There is no backup.
If anyone wants to keep what they built, they take a copy before you begin.

## 1. Delete the server

The exact calls are per provider and are in
[providers.md](providers.md); each section ends with its teardown.

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
dig +short <event>.<domain> A       # returns nothing
dig +short sites.<event>.<domain> A # returns nothing
```

Tell the organiser to check their provider's billing page in a day. A forgotten
disk or a floating IP is the usual way a finished event keeps costing money.
