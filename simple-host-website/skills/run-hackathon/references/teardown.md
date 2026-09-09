# Tearing down

Do both halves. The server alone is not enough.

## Warn first

Deleting the server destroys every entry and all saved data. There is no backup.
If anyone wants to keep what they built, they take a copy before you begin.

## 1. Delete the server

```bash
export UPCLOUD_TOKEN=<their token>
upctl server stop --type hard --wait <uuid>
upctl server delete --delete-storages <uuid>
```

`--delete-storages` matters. Without it the disk survives and keeps billing.

## 2. Remove both DNS records

Delete `<event>` and `sites.<event>` at the registrar.

This is not tidiness. A record still pointing at a released cloud address means
whoever receives that address next is serving content under the organiser's
domain name, and can obtain a valid certificate for it.

## 3. Confirm

```bash
upctl server list          # the event server is gone
dig +short <event>.<domain> A   # returns nothing
```

Tell the organiser to check their provider's billing page in a day. A forgotten
disk or a floating IP is the usual way a finished event keeps costing money.
