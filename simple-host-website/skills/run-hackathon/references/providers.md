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

## Oracle Cloud

Worth it because the always-free tier makes the box cost nothing, permanently
rather than for a trial period. The published image is multi-architecture, so
their ARM shape needs no change.

**Verified 2026-09-09** against a real tenancy, read-only, without creating
anything: authentication, availability domains, both free shapes and the ARM
Ubuntu images all resolve as documented below. A launch has not been run.

### Authentication

Not a bearer token. The organiser creates an API key in the console under
Profile, then My profile, then API keys, and downloads the config fragment. It
gives five values: tenancy OCID, user OCID, key fingerprint, region, and the
private key file. Put them in `~/.oci/config` and drive the `oci` CLI. Do not
sign requests yourself.

If you happen to be running on an Oracle instance already, `--auth
instance_principal` needs no keys at all, but that is not the organiser's case.

### The two always-free shapes

| Shape | Architecture | Free allowance |
|---|---|---|
| `VM.Standard.A1.Flex` | ARM (Ampere) | 4 OCPU and 24 GB total across the tenancy |
| `VM.Standard.E2.1.Micro` | x86 | 2 instances, 1 OCPU and 1 GB each |

Take the ARM shape with 1 OCPU and 6 GB. That is well inside the free allowance
and far more than the instance needs.

### Finding what a launch needs

```bash
export C=<compartment OCID>          # the tenancy OCID works for a simple account
oci iam availability-domain list -c "$C"
oci compute image list -c "$C" \
  --operating-system "Canonical Ubuntu" --operating-system-version "24.04" \
  --shape VM.Standard.A1.Flex --limit 1
oci network subnet list -c "$C"
```

The image names look like `Canonical-Ubuntu-24.04-aarch64-2026.08.25-0`. Take the
newest and use its OCID.

### Launching

```bash
oci compute instance launch -c "$C" \
  --availability-domain "<from the list above>" \
  --shape VM.Standard.A1.Flex \
  --shape-config '{"ocpus":1,"memoryInGBs":6}' \
  --image-id <image OCID> --subnet-id <subnet OCID> \
  --display-name hackathon \
  --metadata '{"ssh_authorized_keys":"<contents of hackathon_key.pub>"}' \
  --assign-public-ip true --wait-for-state RUNNING
```

Then read the public IP:

```bash
oci compute instance list-vnics --instance-id <instance OCID> \
  --query 'data[0]."public-ip"' --raw-output
```

### Two things that will bite

- **Free ARM capacity is frequently exhausted.** "Out of host capacity" is a
  chronic failure on this shape in popular regions. Tell the organiser plainly
  what happened and offer another region or the x86 micro shape. Do not retry in
  a loop; that is how people get rate limited.
- **A default network may not exist.** A brand-new tenancy often has no VCN or
  subnet, and the launch fails on the missing subnet OCID rather than on
  anything to do with this project. If `subnet list` is empty, have the organiser
  create a VCN with the console's "VCN with internet connectivity" wizard first.

### Teardown

```bash
oci compute instance terminate --instance-id <instance OCID> \
  --preserve-boot-volume false --force
```

`--preserve-boot-volume false` matters. A preserved boot volume survives the
instance and counts against the free storage allowance.

## Any other provider

The install step is provider-agnostic: anything giving a fresh Ubuntu 24.04
machine with a public IPv4 and root SSH will run it.

**But teardown is not.** `references/teardown.md` only documents UpCloud. If you
create the server anywhere else, you are responsible for knowing the exact
delete command for that provider before you create anything, and for telling the
organiser what it is. A server nobody knows how to delete keeps billing.

Never choose a plan larger than the smallest available. The organiser is paying,
and a hackathon does not need more.
