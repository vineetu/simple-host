# Creating the server

**If the organiser has no cloud account at all, tell them to open an Oracle Cloud
free account.** It costs nothing, permanently, rather than for a trial period,
and the free x86 machine is one core and a gigabyte, which is exactly what this
is sized for. Two things to say when you recommend it:

- **Approval usually takes about a day.** They give a card for identity only and
  are not charged. Tell them this before they start, so a hackathon on Saturday
  means opening the account on Thursday.
- **Take the x86 shape, not the ARM one.** The free ARM machines are larger and
  more appealing, and they are also the ones that are constantly out of capacity
  in popular regions. The x86 micro is reliably available. Nothing here needs
  more than it offers.

If they already have an account somewhere, use it. Any provider on this page
works and the difference is a few dollars a month.


| Provider | CLI needed? | Approximate monthly cost | Tested end to end? |
|---|---|---|---|
| Hetzner Cloud | No | €6 including IPv4, before tax | No |
| DigitalOcean | No | US$6 | No |
| UpCloud | No | €3 / US$3.50 | Yes |
| Vultr | No | About US$5; confirm current plan price | No |
| Hostinger | No; immediate deletion is not exposed | US$6.49 promotional; short terms cost more | No |
| Oracle Cloud | Yes, `oci` | US$0 on the always-free x86 shape | Yes |

Published documentation checked 2026-09-09. Prices vary by region, tax and term.
UpCloud is the only provider previously used to create, install, use and destroy
an actual server. These rewritten commands have not been run against accounts.

Use the smallest plan with at least 1 GB RAM and a public IPv4. The 512 MB
bargain plans are below the reference server's memory. Installation is in
[install.md](https://simple-host.app/v1/skills/run-hackathon/references/install.md); do not repeat it here.

## Backups: use the provider's, do not build one

Every provider on this page will back the whole machine up on a schedule, for
roughly 20% of the server price. That is the right answer for an event: it
captures the database and the files together, it needs nothing installed, and it
survives the box itself. Offer it to the organiser before creating the server,
because on some providers it is cheaper to enable at creation than to turn on
afterwards.

| Provider | How |
|---|---|
| UpCloud | a `backup_rule` on the storage device at create: `{"interval":"daily","time":"0300","retention":"7"}` |
| Hetzner | `POST /v1/servers/{id}/actions/enable_backup` |
| DigitalOcean | `"backups": true` in the create body, or `POST /v2/droplets/{id}/actions` with `{"type":"enable_backups"}` |
| Vultr | `POST /v2/instances/{id}/backup-schedule` |
| Oracle | boot volume backup policy on the volume |

One caveat worth saying out loud rather than discovering later: a snapshot of a
running machine is crash-consistent, not a clean shutdown. Postgres recovers from
that on start, the same way it recovers from losing power, so it is fine here.
It is not a substitute for a database dump if the data ever really matters.

Participants can also take their own work with them at any time, which is a
different thing and does not depend on the organiser:
`GET /v1/sites/<name>/export.tar.gz` returns the files, the saved JSON and the
collections as one archive.

## Before choosing a provider

Generate a dedicated key on the organiser's laptop, unless it already exists.
Do not overwrite an existing key.

```bash
ssh-keygen -t ed25519 -N '' -f ~/.ssh/hackathon_key -C hackathon
read -r KEY_TYPE KEY_DATA KEY_COMMENT < ~/.ssh/hackathon_key.pub
PUB_KEY="$KEY_TYPE $KEY_DATA"
```

The examples use Bash and curl. No JSON utility is needed: the agent reads the
responses and fills the quoted placeholders from them. Set the chosen token
variable to the organiser's pasted token. Keep server, disk and IP identifiers
for teardown. Run creation once, then repeat the detail GET until it is ready;
a successful POST can mean provisioning has only started.

Teardown blocks are for the end of the event. They destroy the event's data.
These examples create no extra volumes, snapshots or reserved IPs unless stated.
If someone adds those later, record their IDs and delete those event resources
too. Never delete unrelated account resources. Remove event DNS records as
described in [teardown.md](https://simple-host.app/v1/skills/run-hackathon/references/teardown.md), using this file for provider deletion.

## Hetzner Cloud

**Bearer token plus curl is enough. Untested.**

Console → choose the project → Security → API tokens → Generate API token →
name it `hackathon` → choose **Read & Write** → Generate → copy the token.
[Token instructions](https://docs.hetzner.com/cloud/api/getting-started/generating-api-token/).

Use `cx23` in `nbg1`: 2 vCPU, 4 GB RAM, 40 GB disk. It is the smallest
cost-optimised x86 plan. Budget €5.49 plus roughly €0.50 for IPv4, before tax.
Billing is hourly with a monthly cap. The June 2026 price change makes older
€3–4 estimates stale. [Prices](https://docs.hetzner.com/general/infrastructure-and-availability/price-adjustment/),
[IP billing](https://docs.hetzner.com/cloud/billing/faq/).

```bash
HETZNER_TOKEN='<pasted token>'
curl -fsS https://api.hetzner.cloud/v1/ssh_keys \
  -H "Authorization: Bearer $HETZNER_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"hackathon\",\"public_key\":\"$PUB_KEY\"}"
SSH_KEY_ID='<ssh_key.id from response>'

curl -fsS https://api.hetzner.cloud/v1/servers \
  -H "Authorization: Bearer $HETZNER_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"hackathon\",\"server_type\":\"cx23\",\"location\":\"nbg1\",\"image\":\"ubuntu-24.04\",\"ssh_keys\":[$SSH_KEY_ID],\"public_net\":{\"enable_ipv4\":true,\"enable_ipv6\":false}}"
SERVER_ID='<server.id from response>'

curl -fsS "https://api.hetzner.cloud/v1/servers/$SERVER_ID" \
  -H "Authorization: Bearer $HETZNER_TOKEN"
```

Use `server.public_net.ipv4.ip` once `server.status` is `running`. Save
`server.public_net.ipv4.id` as `PRIMARY_IP_ID` before deletion.
[API reference](https://docs.hetzner.cloud/reference/cloud).

**The catch: a Primary IPv4 can survive and keep billing.** Its `auto_delete`
setting determines this. [Primary IP behaviour](https://docs.hetzner.com/cloud/servers/primary-ips/faq/).

```bash
PRIMARY_IP_ID='<server.public_net.ipv4.id>'
curl -fsS -X DELETE "https://api.hetzner.cloud/v1/servers/$SERVER_ID" \
  -H "Authorization: Bearer $HETZNER_TOKEN"
curl -sS -i "https://api.hetzner.cloud/v1/servers/$SERVER_ID" \
  -H "Authorization: Bearer $HETZNER_TOKEN"
curl -sS -i "https://api.hetzner.cloud/v1/primary_ips/$PRIMARY_IP_ID" \
  -H "Authorization: Bearer $HETZNER_TOKEN"
```

Wait for the server GET to return 404. If the IP GET returns 200, delete it;
404 means it was already removed. The included local disk dies with the server.

```bash
curl -fsS -X DELETE "https://api.hetzner.cloud/v1/primary_ips/$PRIMARY_IP_ID" \
  -H "Authorization: Bearer $HETZNER_TOKEN"
curl -fsS -X DELETE "https://api.hetzner.cloud/v1/ssh_keys/$SSH_KEY_ID" \
  -H "Authorization: Bearer $HETZNER_TOKEN"
```

## DigitalOcean

**Bearer token plus curl is enough. Untested.**

Control Panel → Account → API → Tokens → Personal access tokens → Generate
New Token → name it `hackathon` → set expiry after teardown → Full Access →
Generate Token → copy it. [Token instructions](https://docs.digitalocean.com/reference/api/create-personal-access-token/).

Use `s-1vcpu-1gb`: 1 vCPU, 1 GiB RAM, 25 GiB disk, US$6/month.
The US$4 plan has only 512 MiB. Since January 2026, Droplets use per-second
billing with a minimum of 60 seconds or US$0.01, whichever is higher, and a
monthly cap. [Pricing](https://www.digitalocean.com/pricing/droplets).

```bash
DO_TOKEN='<pasted token>'
curl -fsS https://api.digitalocean.com/v2/account/keys \
  -H "Authorization: Bearer $DO_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"hackathon\",\"public_key\":\"$PUB_KEY\"}"
SSH_KEY_ID='<ssh_key.id from response>'

curl -fsS https://api.digitalocean.com/v2/droplets \
  -H "Authorization: Bearer $DO_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"hackathon\",\"region\":\"nyc3\",\"size\":\"s-1vcpu-1gb\",\"image\":\"ubuntu-24-04-x64\",\"ssh_keys\":[$SSH_KEY_ID],\"backups\":false}"
SERVER_ID='<droplet.id from response>'

curl -fsS "https://api.digitalocean.com/v2/droplets/$SERVER_ID" \
  -H "Authorization: Bearer $DO_TOKEN"
```

When `droplet.status` is `active`, take `ip_address` from the entry in
`droplet.networks.v4` whose `type` is `public`.

**The catch: deleting just the Droplet leaves separately billed resources.**
For this fresh event server, inspect the associated-resource list, then destroy
it and its event-only resources. The deletion is asynchronous; check for a
non-null `completed_at` and zero `failures`.
[Droplet API](https://docs.digitalocean.com/products/droplets/reference/api/droplets/).

```bash
curl -fsS "https://api.digitalocean.com/v2/droplets/$SERVER_ID/destroy_with_associated_resources" \
  -H "Authorization: Bearer $DO_TOKEN"
curl -fsS -X DELETE "https://api.digitalocean.com/v2/droplets/$SERVER_ID/destroy_with_associated_resources/dangerous" \
  -H "Authorization: Bearer $DO_TOKEN" -H 'X-Dangerous: true'
curl -fsS "https://api.digitalocean.com/v2/droplets/$SERVER_ID/destroy_with_associated_resources/status" \
  -H "Authorization: Bearer $DO_TOKEN"
curl -fsS -X DELETE "https://api.digitalocean.com/v2/account/keys/$SSH_KEY_ID" \
  -H "Authorization: Bearer $DO_TOKEN"
```

## UpCloud

**Bearer token plus curl is enough. Tested end to end previously.**

Control Panel → Account → API Tokens → Add new API token → name it `hackathon`
→ expiry after teardown → allow the laptop's public IP → Create API token →
copy it. [Token instructions](https://upcloud.com/docs/guides/managing-api-tokens/).

`DEV-1xCPU-1GB` and `STARTER-1xCPU-1GB` were both returned by the live API, but
**Both refuse `maxiops` storage.** The small plans take `standard` only, and
asking for anything else answers `TIER_INVALID` with a message naming the tier
rather than the plan, so it reads as a storage problem when it is really a plan
one. Use `"tier":"standard"`, exactly as below.
Use Starter: 1 vCPU, 1 GB RAM, 10 GB Standard disk and public IPv4, about
€3 / US$3.50 monthly, billed hourly. Published docs now favour Starter over
Developer plans. [Configurations](https://upcloud.com/docs/products/cloud-servers/configurations/),
[prices](https://calc.upcloud.com/).

The bearer-token account, plan and zone GETs were verified on 2026-09-09.
There were 15 zones, including `us-nyc1`, `us-sjo1`, `de-fra1`, `uk-lon1` and
`au-syd1`. Read the current template UUID instead of guessing it.

**Two templates match "Ubuntu Server 24.04 LTS".** One of them carries NVIDIA
drivers and CUDA and needs 20 GB, which will not fit the 10 GB disk this page
tells you to order. Take the plain one.

```bash
UPCLOUD_TOKEN='<pasted token>'
curl -fsS https://api.upcloud.com/1.3/account -H "Authorization: Bearer $UPCLOUD_TOKEN"
curl -fsS https://api.upcloud.com/1.3/plan -H "Authorization: Bearer $UPCLOUD_TOKEN"
curl -fsS https://api.upcloud.com/1.3/zone -H "Authorization: Bearer $UPCLOUD_TOKEN"
curl -fsS https://api.upcloud.com/1.3/storage/template \
  -H "Authorization: Bearer $UPCLOUD_TOKEN"
TEMPLATE_ID='<uuid of Ubuntu Server 24.04 LTS x86_64 template>'

curl -fsS https://api.upcloud.com/1.3/server \
  -H "Authorization: Bearer $UPCLOUD_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"server\":{\"zone\":\"us-nyc1\",\"title\":\"hackathon\",\"hostname\":\"hackathon\",\"plan\":\"STARTER-1xCPU-1GB\",\"metadata\":\"yes\",\"login_user\":{\"username\":\"root\",\"ssh_keys\":{\"ssh_key\":[\"$PUB_KEY\"]}},\"storage_devices\":{\"storage_device\":[{\"action\":\"clone\",\"storage\":\"$TEMPLATE_ID\",\"title\":\"hackathon-root\",\"size\":10,\"tier\":\"standard\"}]},\"networking\":{\"interfaces\":{\"interface\":[{\"type\":\"public\",\"ip_addresses\":{\"ip_address\":[{\"family\":\"IPv4\"}]}}]}}}}"
SERVER_ID='<server.uuid from response>'

curl -fsS "https://api.upcloud.com/1.3/server/$SERVER_ID" \
  -H "Authorization: Bearer $UPCLOUD_TOKEN"
```

The key is supplied inline; no registration call is needed. Wait for
`server.state` to be `started`. In `server.ip_addresses.ip_address`, take
`address` where `access` is `public` and `family` is `IPv4`, never the utility
`10.x` address. [Server API](https://developers.upcloud.com/1.3/8-servers/).

**The catch: disks survive an ordinary server DELETE.** Stop first, wait for
`stopped`, then include storage and backup deletion.

```bash
curl -fsS "https://api.upcloud.com/1.3/server/$SERVER_ID/stop" \
  -H "Authorization: Bearer $UPCLOUD_TOKEN" -H 'Content-Type: application/json' \
  -d '{"stop_server":{"stop_type":"soft","timeout":60}}'
curl -fsS "https://api.upcloud.com/1.3/server/$SERVER_ID" \
  -H "Authorization: Bearer $UPCLOUD_TOKEN"
```

Only after `server.state` is `stopped`:

```bash
curl -fsS -X DELETE "https://api.upcloud.com/1.3/server/$SERVER_ID?storages=1&backups=delete" \
  -H "Authorization: Bearer $UPCLOUD_TOKEN"
curl -sS -i "https://api.upcloud.com/1.3/server/$SERVER_ID" \
  -H "Authorization: Bearer $UPCLOUD_TOKEN"
```

Expect 404 on the final GET. Attached disks and their backups are deleted;
the server's addresses are released.

## Vultr

**Bearer token plus curl is enough. Untested.**

Console → Account → API → Enable API → copy API Key. Under Access Control,
allow the laptop's public IP. Some newer organisation accounts use IAM → Users;
console labels may differ. [API access instructions](https://docs.vultr.com/support/platform/users/how-can-i-manage-api-access-for-users).

Use `vc2-1c-1gb`: 1 vCPU, 1 GB RAM, 25 GB disk, historically about US$5/month,
billed hourly. Current price was not independently confirmed; read `monthly_cost`
from `/plans` before creating. Skip IPv6-only and 512 MB plans.
[Plan example](https://docs.vultr.com/how-to-provision-cloud-infrastructure-on-vultr-using-terraform).

```bash
VULTR_TOKEN='<pasted token>'
curl -fsS 'https://api.vultr.com/v2/plans?type=vc2&per_page=100' \
  -H "Authorization: Bearer $VULTR_TOKEN"
curl -fsS 'https://api.vultr.com/v2/os?per_page=100' \
  -H "Authorization: Bearer $VULTR_TOKEN"
curl -fsS 'https://api.vultr.com/v2/regions/ewr/availability?type=vc2' \
  -H "Authorization: Bearer $VULTR_TOKEN"
OS_ID='<id for Ubuntu 24.04 x64 in os list>'

curl -fsS https://api.vultr.com/v2/ssh-keys \
  -H "Authorization: Bearer $VULTR_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"name\":\"hackathon\",\"ssh_key\":\"$PUB_KEY\"}"
SSH_KEY_ID='<ssh_key.id from response>'

curl -fsS https://api.vultr.com/v2/instances \
  -H "Authorization: Bearer $VULTR_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"region\":\"ewr\",\"plan\":\"vc2-1c-1gb\",\"os_id\":$OS_ID,\"sshkey_id\":[\"$SSH_KEY_ID\"],\"label\":\"hackathon\",\"hostname\":\"hackathon\",\"backups\":\"disabled\"}"
SERVER_ID='<instance.id from response>'

curl -fsS "https://api.vultr.com/v2/instances/$SERVER_ID" \
  -H "Authorization: Bearer $VULTR_TOKEN"
```

Follow `meta.links.next` if a list is paginated. Confirm the plan is available
in `ewr` before creating. Wait for `instance.status` = `active` and
`server_status` = `ok`; use `instance.main_ip`, not an initial `0.0.0.0`.
[API reference](https://www.vultr.com/api/).

**The catch: an enabled API key still fails if the caller's IP is not allowed.**
The allowlist needs the laptop's public address, not the new server's address.

```bash
curl -fsS -X DELETE "https://api.vultr.com/v2/instances/$SERVER_ID" \
  -H "Authorization: Bearer $VULTR_TOKEN"
curl -sS -i "https://api.vultr.com/v2/instances/$SERVER_ID" \
  -H "Authorization: Bearer $VULTR_TOKEN"
curl -fsS -X DELETE "https://api.vultr.com/v2/ssh-keys/$SSH_KEY_ID" \
  -H "Authorization: Bearer $VULTR_TOKEN"
```

Expect 404 for the instance. Its local disk and ordinary IPv4 go with it.
This recipe disables backups and creates no separately billed storage or
reserved IP. Stopping an instance does not end billing.

## Hostinger

**Bearer token plus curl is enough for the published API. Untested. Immediate
VPS destruction is not exposed, so this does not meet a fully automated lifecycle.**

hPanel → Dev tools → API → Generate API token → name it `hackathon` → choose
expiry after teardown → generate → copy. [Token instructions](https://www.hostinger.com/support/how-to-set-up-web-hosting-mcp-on-local-ides/).

KVM 1 has 1 vCPU, 4 GB RAM and 50 GB disk. The advertised US$6.49/month is
promotional; the page lists US$11.99/month renewal on a two-year term. A one-month
purchase costs more; get its actual price from the catalog. This is prepaid
subscription billing, **not hourly**. [Prices](https://www.hostinger.com/vps-hosting),
[billing periods](https://www.hostinger.com/support/1583589-how-to-pay-for-hostinger-services-in-advance/).

```bash
HOSTINGER_TOKEN='<pasted token>'
curl -fsS 'https://developers.hostinger.com/api/billing/v1/catalog?category=VPS' \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
curl -fsS https://developers.hostinger.com/api/vps/v1/templates \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
curl -fsS https://developers.hostinger.com/api/vps/v1/data-centers \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
ITEM_ID='<KVM 1 one-month price item id from catalog>'
TEMPLATE_ID='<plain Ubuntu 24.04 template id>'
DATA_CENTER_ID='<available data center id>'
```

Catalog prices are cents. Select the price item, not just the product name.
The following POST purchases a subscription using the default payment method.
It is not a free setup call. The key is supplied inline.

```bash
curl -fsS https://developers.hostinger.com/api/vps/v1/virtual-machines \
  -H "Authorization: Bearer $HOSTINGER_TOKEN" -H 'Content-Type: application/json' \
  -d "{\"item_id\":\"$ITEM_ID\",\"setup\":{\"template_id\":$TEMPLATE_ID,\"data_center_id\":$DATA_CENTER_ID,\"enable_backups\":false,\"public_key\":{\"name\":\"hackathon\",\"key\":\"$PUB_KEY\"}}}"
curl -fsS https://developers.hostinger.com/api/vps/v1/virtual-machines \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
SERVER_ID='<id of the newly purchased VM>'
curl -fsS "https://developers.hostinger.com/api/vps/v1/virtual-machines/$SERVER_ID" \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
```

Wait for `state` = `running`; read `ipv4[].address`. Save `subscription_id`.
[Published OpenAPI](https://github.com/hostinger/api/blob/main/openapi.json).

**The catch: stopping the VPS does not cancel its subscription.** There is no
published `DELETE /virtual-machines/{id}`. Disable renewal for this VM's
subscription, then verify `is_auto_renewed` is false in the subscription list.

```bash
SUBSCRIPTION_ID='<subscription_id from VM details>'
curl -fsS -X POST "https://developers.hostinger.com/api/vps/v1/virtual-machines/$SERVER_ID/stop" \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
curl -fsS -X DELETE "https://developers.hostinger.com/api/billing/v1/subscriptions/$SUBSCRIPTION_ID/auto-renewal/disable" \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
curl -fsS https://developers.hostinger.com/api/billing/v1/subscriptions \
  -H "Authorization: Bearer $HOSTINGER_TOKEN"
```

This prevents renewal; it does not destroy the VM or refund the prepaid term.
For immediate deletion, the organiser must request cancellation and data deletion
through hPanel support. Confirm removal there; do not report the server destroyed
just because renewal is off. [API reference](https://developers.hostinger.com/).

## Oracle Cloud

**The `oci` CLI is needed in practice. Tested end to end 2026-09-10.** Every API request
uses an RSA signature, not a bearer token. Plain curl would require implementing
request signing. Install `oci` using Oracle's
[installation instructions](https://docs.oracle.com/en-us/iaas/Content/API/SDKDocs/cliinstall.htm).

Run end to end on 2026-09-10: launched, installed, hostnames claimed,
certificates issued, a participant published, analytics counted, terminated.
On `VM.Standard.E2.1.Micro`, the always-free x86 shape, at no cost.

Three things that will catch you, in the order they bite:

- **A launch refused for want of permission returns `NotAuthorizedOrNotFound`**,
  which reads exactly like a mistyped image or subnet id and is neither. Check
  the policy first. Launching needs `manage instance-family`; `use
  instance-family` only permits stopping and starting something that exists.
  Add `use volume-family` for the boot volume and `use virtual-network-family`
  to attach to a subnet and take a public address.
- **Oracle instances carry their own firewall, and Ubuntu images ship with it
  closed.** Opening ports on the subnet's security list is not enough: run
  `iptables -I INPUT -p tcp --dport 80 -j ACCEPT` and the same for 443 on the
  instance, then persist it. Nothing else on this page needs that step, and
  skipping it looks exactly like a DNS problem.
- **You log in as `ubuntu`, not `root`.** The install script needs `sudo`.

Console → profile icon → My profile → API keys → Add API key → Generate API key
pair → Download private key → Add → View configuration file. Console labels may
vary with the identity-domain view. Save the configuration in `~/.oci/config`
and set `key_file` to the downloaded private key. It contains the tenancy OCID,
user OCID, fingerprint and region. This API signing key is separate from the
server's SSH key. [API key setup](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/apisigningkey.htm).

**Use `VM.Standard.E2.1.Micro`.** Two of them are always free, each 1 OCPU and
1 GB, and that allowance is independent of the ARM pool, so it is free whatever
else the tenancy is running. One core and a gigabyte is what everything here is
sized for.

`VM.Standard.A1.Flex` is the larger, more tempting ARM shape, and it is free
only within **4 OCPU and 24 GB across the entire tenancy**. Anything else already
running eats that, and a launch over the line is billed rather than refused. Do
not reach for it to get a bigger machine without adding up the allowance first,
and never on an account someone opened because they have no money.

Use an eligible image and the home region. [Always Free limits](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm).



```bash
C='<compartment OCID; tenancy OCID works for a simple account>'
oci iam availability-domain list -c "$C"
oci compute image list -c "$C" \
  --operating-system 'Canonical Ubuntu' --operating-system-version '24.04' \
  --shape VM.Standard.E2.1.Micro --sort-by TIMECREATED --sort-order DESC --all
oci network subnet list -c "$C" --all
AD='<availability-domain name>'
IMAGE_ID='<newest compatible Ubuntu 24.04 ARM image OCID>'
SUBNET_ID='<public subnet OCID>'
```

ARM images have names like `Canonical-Ubuntu-24.04-aarch64-2026.08.25-0`.
The published application image supports ARM. No separate SSH-key registration
is needed.

```bash
oci compute instance launch -c "$C" \
  --availability-domain "$AD" --shape VM.Standard.E2.1.Micro \
  --image-id "$IMAGE_ID" --subnet-id "$SUBNET_ID" \
  --display-name hackathon --ssh-authorized-keys-file ~/.ssh/hackathon_key.pub \
  --assign-public-ip true --wait-for-state RUNNING
SERVER_ID='<data.id from launch response>'
oci compute instance list-vnics --instance-id "$SERVER_ID" \
  --query 'data[0]."public-ip"' --raw-output
```

Ubuntu uses SSH user `ubuntu`, with `sudo` for root work.
The subnet needs an internet gateway route and ingress for SSH, HTTP and HTTPS.

**The catch: free ARM capacity is frequently exhausted.** On “Out of host
capacity”, explain the failure and consider another eligible availability domain
or the x86 micro shape with an x86 image. Do not retry in a loop. Another region
may not qualify for Always Free.

A fresh tenancy often has no VCN or subnet. If the subnet list is empty, the
organiser must use Networking → Virtual cloud networks → Start VCN Wizard →
VCN with Internet Connectivity before launch. A missing subnet ID is an account
network setup issue, not a project failure. Do not pretend a token alone solves it.

```bash
oci compute instance terminate --instance-id "$SERVER_ID" \
  --preserve-boot-volume false --force --wait-for-state TERMINATED
```

`--preserve-boot-volume false` matters: otherwise the disk survives and consumes
the free storage allowance. The ephemeral public IP is released. This launch
adds no data volumes, backups or reserved IPs. Keep any pre-existing network.
[Termination reference](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/compute/instance/terminate.html).
