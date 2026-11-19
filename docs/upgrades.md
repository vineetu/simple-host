# Upgrade and paired restore

Use the upgrade script from the **candidate release**, with an existing Compose
installation. That release must include the `upgrade` command. First installs
still use `install.sh`; they record their release only after schema verification.
If verification is interrupted, schema creation's durable first-install marker
lets a successful retry finish recording the release. Preparation that has not
created an app container continues through the first-install path.
An ordinary `migrate` or first recording of an older installation does not change
the recorded release.

## Supported starting versions

The first shared release supports these starting points:

- Simple Host **v0.7.22**.
- Simple Hack **hack-v0.8.20**.

Upgrade older installations to that family's tagged release first. Later release notes must list their supported starting releases;
the command accepts a recorded release only when it is on that list.

An older database has no recorded release. Its migration ledger must contain
every tracked migration of the tagged release above, plus the baseline account,
site, key and connector tables. Baseline migrations were never recorded and are
not required as ledger entries. Extra ledger entries are allowed. This check is
read-only and precedes migrations and first product recording.

## Compose

Backups include private configuration. Keep the installation and its `backups/`
directory private, retain the original images, and copy complete backups to your
normal protected backup storage. Local builds without registry digests must stay
in Docker's local image store; a local image ID cannot be pulled from a registry.
Do not change Postgres's major version as part of this upgrade.

From the unpacked candidate package, for Simple Hack:

```sh
sudo bash deploy/hack/standalone/upgrade.sh --to "$VERSION" --image "ghcr.io/vineetu/simple-hack:$VERSION"
```

For Simple Host, re-running the candidate `deploy/install/install.sh` delegates
an existing installation to the shared script before rewriting configuration.
To call it directly, with `VERSION` set to the candidate release:

```sh
sudo bash deploy/shared/upgrade.sh --dir /opt/simple-host --product simple-host --to "$VERSION" --image "ghcr.io/vineetu/simple-host:$VERSION" --compose compose.yaml --caddyfile deploy/compose/Caddyfile
```

The selected image supplies the target release; an explicit `--to` must match
its version. The Host installer applies explicit `--host`, `--content`, `--email`,
`--max-site-mb` and `--keep-versions` settings after securing the original backup,
preserving unspecified settings. It also refreshes the Caddyfile from the selected
release. `--with kv-move` opts into the named data step when that
release offers it; the script passes it to the upgrade command.
The installed Compose configuration retains environment references, so later
`.env` changes still apply when services are recreated. Configuration is
published by a synced atomic rename into a new private file.

The script stops every server and writer in this Compose project, including
one-off containers, `web`, `static`, and Hack's `release` service. It takes the
exclusive data-directory maintenance lock before backing up; a remaining server
or maintenance command refuses the operation. The database stays running.

One attempt is named
`<product>-<from>-<to>-<UTC time of its first start>`. `backups/CURRENT` names
`upgrade-<attempt>/`, containing:

- `database.dump`: a custom-format `pg_dump` of the stopped installation.
- `data.tar`: the whole data volume, including SQLite files and WAL files,
  uploaded files.
- `env` and `compose.yaml`: the configuration before the upgrade.
- `Caddyfile`: the Host web configuration before the upgrade, when present.
- `images.json`: the resolved original Compose configuration with immutable image
  digests and the original container image IDs, including images that were
  actually running. Restore uses the saved local IDs even if a tag has moved.
- `manifest.json`: attempt, product, starting and target releases, install
  identity (empty for older installs without one), data volume, running services,
  image digests, sizes and SHA-256 checksums of the backup files.
- `COMPLETE`: written last, binding the manifest's checksum.

The command runs with `--attempt` and `-record-product`. On success the script
starts the app, requires `/readyz`, and starts the previously running web and
Static services. Only then does it move `CURRENT` to `LAST`. Both the backup and
older backups remain. The next upgrade creates a fresh attempt and captures data
written since the previous one.

On failure, keep the installation stopped and rerun the **same candidate
command**. A complete backup is reused only when product, starting release,
target release and every checksum match. It is never replaced with a backup of
partly upgraded data. A different target is refused. An interrupted backup with
no `COMPLETE` is renamed with `-incomplete` and a fresh backup is taken; no data
step could have run from that incomplete attempt.
The original running-service set is saved before stopping anything and retained
across incomplete-backup retries. Under the installation lock, retries reclaim
only this installation's abandoned backup-lock container after a killed script.

## Restore

The failure message prints the restore command. The same command also restores
the last successfully closed attempt when there is no `CURRENT`:

```sh
sudo bash /opt/simple-host/shared-upgrade.sh --dir /opt/simple-host --product simple-host --restore
```

For Hack:

```sh
sudo bash /opt/simple-hack/shared-upgrade.sh --dir /opt/simple-hack --product simple-hack --restore
```

Restore stops every writer, checks the manifest and checksums, and takes the
exclusive maintenance lock. It empties the database schemas before `pg_restore`,
empties the data volume while preserving the held lock inode, and extracts the
saved archive. It puts back the original environment, Compose file and Host
Caddyfile and starts the image digests saved in `images.json`, including Postgres
when its container must be recreated, bypassing migration/release services.
After `/readyz` succeeds it closes the attempt to `LAST`. Database, files,
configuration and program are restored together; installing an older image
alone is not a data rollback. A later manual Compose start must also use those
saved image digests if the original tags have moved.

## Kubernetes

Use the same attempt and complete-backup protocol for Helm. Do this in a quiet
window, one installation at a time:

1. Before stopping pods, save the installed chart/version, `helm get values`,
   environment/Secret manifests and the actual running `imageID` digests into a
   private attempt directory. Stop or suspend release/migration Jobs and every
   other writer. Scale the app and any Static server to zero and wait for their
   pods to terminate. A failing readiness probe alone proves nothing.
2. Mount the data PVC in one maintenance pod, as its owning UID. Hold the
   exclusive `.maintenance.lock` for the whole paired backup; refuse if it is
   held elsewhere. Save `pg_dump -Fc`, a tar archive of the whole PVC, saved
   configuration and image digests in the attempt directory. Record install
   identity, product, from/to versions, file sizes and SHA-256 checksums in
   `manifest.json`; write `COMPLETE` last and then retain this attempt as
   `CURRENT`. Do not overwrite a complete backup on retry. Quarantine an
   incomplete backup before taking a new one.
3. Run the candidate's `upgrade --attempt` command with the same PVC, database
   and configuration, and with the exclusive maintenance lock. Release the
   backup pod's lock before the command takes it. No server starts in between.
   Resume the same attempt after failure. The command validates compatibility
   before recording the product or migrating.
4. Run `helm upgrade` with the candidate chart and saved values, keeping one
   replica. Start servers only after upgrade verification, and wait for
   `/readyz`. Move `CURRENT` to `LAST` only after success. Each later upgrade
   takes its own paired backup.
5. To restore, scale every writer to zero again, stop Jobs, verify the original
   manifest and checksums, and hold the exclusive PVC lock. Restore the dump into
   empty database schemas and the archive into the emptied PVC. Restore the
   saved chart, environment/Secrets and values with the **saved image digests**,
   without rerunning migrations. Release the lock, start one replica and require
   `/readyz` before closing the attempt. A Helm rollback by itself does not
   restore the data.
