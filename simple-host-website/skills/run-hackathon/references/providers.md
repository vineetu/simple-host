# Creating the server

Smallest Ubuntu 24.04 plan at any provider. One CPU and 1 GB is enough: it is
what the reference instance runs on.

## Generate an SSH key first, on the organiser's machine

```bash
ssh-keygen -t ed25519 -N "" -f ~/.ssh/hackathon_key -C "hackathon"
```

Pass the public half at creation. Never ask for a root password.

## UpCloud (tested)

Install `upctl`, then:

```bash
export UPCLOUD_TOKEN=<their token>
upctl server create \
  --title hackathon --hostname hackathon \
  --zone us-nyc1 --plan 1xCPU-1GB \
  --os "Ubuntu Server 24.04 LTS (Noble Numbat)" \
  --ssh-keys ~/.ssh/hackathon_key.pub --wait
```

Read the **public** IPv4 from the output. UpCloud lists a private `10.x` address
too; DNS must point at the public one.

Zones: `us-nyc1`, `us-sjo1`, `uk-lon1`, `de-fra1`, `sg-sin1` and others. Pick one
near the participants.

## Oracle Cloud (not yet tested)

Worth it because their always-free Ampere tier makes the box cost nothing. Two
differences to plan for:

- Authentication is an RSA key pair plus tenancy, user, fingerprint, region and
  compartment identifiers, not a single token. Drive the `oci` CLI rather than
  signing requests yourself.
- Free ARM capacity is frequently exhausted. "Out of host capacity" is common.
  If it happens, tell the organiser plainly and offer another region or the paid
  micro shape. Do not retry silently.

The published image is multi-architecture, so their ARM shape needs no change.

## Any other provider

The install step is provider-agnostic: anything giving a fresh Ubuntu 24.04
machine with a public IPv4 and root SSH will run it.

**But teardown is not.** `references/teardown.md` only documents UpCloud. If you
create the server anywhere else, you are responsible for knowing the exact
delete command for that provider before you create anything, and for telling the
organiser what it is. A server nobody knows how to delete keeps billing.

Never choose a plan larger than the smallest available. The organiser is paying,
and a hackathon does not need more.
