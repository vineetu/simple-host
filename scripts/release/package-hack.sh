#!/usr/bin/env bash
# Stage the installation download with the charts and image references for this tag.
set -euo pipefail
package=$1
tag=$2
commit=$3
repository=$4
host_image=$5
hack_base_image=$6
hack_image=$7
version=${tag#v}

mkdir -p "$package/deploy/"{hack,digitalocean/droplet/scripts,shared} "$package/docs/platforms/assets" "$package/db"
cp -a deploy/hack/standalone "$package/deploy/hack/"
cp -a deploy/kubernetes "$package/deploy/"
cp deploy/shared/upgrade.sh "$package/deploy/shared/"
if [[ $tag == v*-rc.* ]]; then
  mkdir -p "$package/deploy/"{compose,install}
  cp deploy/install/install.sh "$package/deploy/install/"
  cp compose.yaml "$package/"
  cp deploy/compose/Caddyfile "$package/deploy/compose/"
fi
cp deploy/digitalocean/droplet/scripts/{010-docker,014-ufw-http,900-cleanup,950-final-cleanup,999-img_check}.sh \
  "$package/deploy/digitalocean/droplet/scripts/"
{
  cat host/db/schema.sql
  printf '\n'
  cat products/hack/db/schema.sql
} > "$package/db/schema.sql"
cp docs/{configuration,kubernetes,upgrades}.md "$package/docs/"
cp docs/platforms/simple-hack-*.md "$package/docs/platforms/"
cp docs/platforms/assets/simple-hack-get-started.png "$package/docs/platforms/assets/"

python3 - "$package" "$version" "$repository" "$host_image" "$hack_base_image" "$hack_image" <<'PY'
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import sys

package, version, repository, host_image, hack_base_image, hack_image = sys.argv[1:]
package = Path(package)
standalone = package / "deploy/hack/standalone"
compose = (standalone / "compose.yaml").read_text()
source_image = re.search(r"\$\{SIMPLE_HACK_IMAGE:-([^}]+)\}", compose)[1]
source_version = source_image.rsplit(":", 1)[1]
for name in ("install.sh", "upgrade.sh", "compose.yaml", ".env.example",
             "digitalocean/template.json", "digitalocean/prepare.sh"):
    path = standalone / name
    body = path.read_text().replace(source_image, hack_image)
    body = body.replace(f'"{source_version}"', f'"{version}"')
    path.write_text(body)

docs = package / "docs/platforms"
guide = docs / "simple-hack-standalone.md"
body = guide.read_text().replace(f"VERSION={source_version}", f"VERSION={version}")
download = r"curl -fLO https://github\.com/[^/]+/[^/]+/releases/download/(?:hack-)?v\$VERSION/\S+"
if "-rc." in version:
    instruction = "# Download simple-hack-$VERSION.tar.gz from the release-candidate workflow artifact."
    helper = package / "deploy/shared/upgrade.sh"
    stable_pattern = r"([0-9]+\.[0-9]+\.[0-9]+)(?![A-Za-z0-9_.-])"
    parser = helper.read_text()
    # Only the older helper needs an archive-specific candidate pattern.
    if stable_pattern in parser:
        helper.write_text(parser.replace(stable_pattern, f"({re.escape(version)})(?![A-Za-z0-9_.-])"))

    ocean = docs / "simple-hack-digitalocean.md"
    snapshot = "### Build the image"
    body_rc = ocean.read_text()
    if body_rc.count(snapshot) != 1:
        raise SystemExit(f"expected one snapshot procedure in {ocean}")
    ocean.write_text(body_rc.replace(snapshot, """### Rehearse a private release candidate

On the target droplet, run `sudo docker login ghcr.io` and sign in with an account
that can read the candidate image. Use the [standalone installer](simple-hack-standalone.md#install)
from this archive. Packer pulls the image on a separate temporary droplet.

### Build a snapshot with an image the temporary droplet can pull"""))
else:
    instruction = f"curl -fLO https://github.com/{repository}/releases/download/v$VERSION/simple-hack-$VERSION.tar.gz"
shutil.copy2(package / "deploy/shared/upgrade.sh", standalone / "shared-upgrade.sh")
body, count = re.subn(download, instruction, body)
if count != 1:
    raise SystemExit(f"expected one release download command in {guide}")
body = body.replace(source_image.rsplit(":", 1)[0] + ":$VERSION",
                    hack_image.rsplit(":", 1)[0] + ":$VERSION")
guide.write_text(body)

upgrades = package / "docs/upgrades.md"
hack_args = f'--to "$VERSION" --image "{source_image.rsplit(":", 1)[0]}:$VERSION"'
body = upgrades.read_text().replace(hack_args, f'--to "{version}" --image "{hack_image}"')
host_intro = ("For Simple Host, re-running the candidate `deploy/install/install.sh` delegates\n"
              "an existing installation to the shared script before rewriting configuration.\n"
              "To call it directly, with `VERSION` set to the candidate release:")
if body.count(host_intro) != 1:
    raise SystemExit(f"expected one Host Compose section in {upgrades}")
checkout = f"[source checkout for `v{version}`](https://github.com/{repository}/tree/v{version})"
if "-rc." in version:
    body = body.replace("## Compose\n", f"""## Fresh Compose install

From the root of the unpacked RC archive on a fresh Host machine, run the
tagged installer. It opens the setup page where you can configure the hostnames.

```sh
sudo bash deploy/install/install.sh --ref v{version} --image {host_image}
```

For a fresh Hack installation, follow the [standalone install guide](platforms/simple-hack-standalone.md#install).

## Compose
""")
    body = body.replace(host_intro, "For an existing Simple Host installation, run the packaged helper from the root of this unpacked RC archive:")
    body = body.replace('--to "$VERSION"', f'--to "{version}"')
else:
    body = body.replace(host_intro, f"Use the {checkout} for Simple Host.\n\n" + host_intro)
body = body.replace("ghcr.io/vineetu/simple-host:$VERSION", host_image)
host_command, = (shlex.split(line) for line in body.splitlines()
                 if line.startswith("sudo bash deploy/shared/upgrade.sh "))
if "-rc." not in version:
    source_paths = ["deploy/install/install.sh", host_command[2]]
    for flag in ("--compose", "--caddyfile"):
        source_paths.append(host_command[host_command.index(flag) + 1])
    for path in source_paths:
        subprocess.run(["git", "cat-file", "-e", f"HEAD:{path}"], check=True)
upgrades.write_text(body)

kubernetes = package / "docs/kubernetes.md"
body = kubernetes.read_text()
body, count = re.subn(r"^`deploy/shared/versions\.env` pins .*?(?=^The manifests expect)", f"""The bundled chart defaults select the matching Simple Host and Simple Hack images
for `v{version}`. To regenerate the plain manifests after editing the example values,
run `bash deploy/kubernetes/render.sh` from the root of this archive.
""", body, flags=re.M | re.S)
if count != 1:
    raise SystemExit(f"expected one chart setup paragraph in {kubernetes}")
host_upgrade = ("Upgrade with `helm upgrade simple-host deploy/kubernetes/simple-host -n simple-host\n"
                "-f host-values.yaml`.")
if body.count(host_upgrade) != 1:
    raise SystemExit(f"expected one Host upgrade command in {kubernetes}")
body = body.replace(host_upgrade, "Follow the [paired Kubernetes upgrade procedure](upgrades.md#kubernetes).")
old_intro = "A release is a new image tag. Migrations run on start, so an upgrade is:"
old_override = " \\\n  --set image.tag=<published-release-tag>"
if body.count(old_intro) != 1 or body.count(old_override) != 1:
    raise SystemExit(f"expected one Helm upgrade example in {kubernetes}")
body = body.replace(old_intro, "Follow the [paired Kubernetes upgrade procedure](upgrades.md#kubernetes)\n"
                    "before upgrading Helm. Use the candidate chart and leave `image.repository`\n"
                    "and `image.tag` at its defaults to select the matching product image:")
body = body.replace(old_override, "")
body, count = re.subn(r"^To prepare a database by hand instead .*?^```sh\nkubectl -n simple-hack run migrate .*?^```\n\n",
                      "", body, flags=re.M | re.S)
install_anchor = "### Pod security"
if count != 1 or body.count(install_anchor) != 1:
    raise SystemExit(f"expected a manual initialization example and install section in {kubernetes}")
examples = []
kind_tests = []
for product, label, image in (("simple-host", "Simple Host", host_image),
                              ("simple-hack", "Simple Hack", hack_base_image)):
    examples.append(f"""**{label}**

```sh
overrides=$(kubectl -n {product} get deployment {product} -o json | jq -nce '
  input.spec.template.spec as $pod | $pod.containers[] | select(.name == "{product}") |
  (first(.env[]? | select(.name == "DB_DSN") | .valueFrom.secretKeyRef) //
   {{name: first(.envFrom[] | .secretRef.name // empty), key: "DB_DSN"}}) as $ref |
  {{apiVersion: "v1", spec: {{automountServiceAccountToken: false, securityContext: $pod.securityContext,
    imagePullSecrets: ($pod.imagePullSecrets // []), containers: [{{name: "migrate", securityContext,
    env: [{{name: "DB_DSN", valueFrom: {{secretKeyRef: $ref}}}}]}}]}}}}') &&
kubectl -n {product} run migrate --rm -it --restart=Never --image {image} \\
  --override-type=strategic --overrides="$overrides" -- migrate -init -record-product
```""")
    kind_tests.append(f"docker pull {image}\nIMAGE={image} bash deploy/kubernetes/tests/kind-check.sh {product}")
body = body.replace(install_anchor, """### Manual initialization for a fresh install

Use this option only for an empty database. For an existing installation,
follow the [paired Kubernetes upgrade procedure](upgrades.md#kubernetes).
For manual initialization, set `migrateOnStart: false` in your values before
running `helm install`. After Helm creates the Deployment, run the command for
your product before waiting for rollout. It uses `jq` to read the database
Secret reference from the Deployment. Adjust the namespace and Deployment name
if you chose different names.

""" + "\n\n".join(examples) + "\n\n" + install_anchor)
test_intro = "- `bash deploy/kubernetes/test-chart.sh` checks both charts"
body = body.replace(test_intro, f"\n## Test the charts\n\nRun both the chart and kind tests from the root of the {checkout}. "
                    "The chart check reads the shared pins and database fixture there. "
                    "For a candidate rehearsal, pull each image into local Docker and pass it to its kind test:\n\n"
                    "```sh\n" + "\n".join(kind_tests) + "\n```\n\n" + test_intro)
kubernetes.write_text(body)

charts = package / "deploy/kubernetes"
for product, image in (("simple-host", host_image), ("simple-hack", hack_base_image)):
    chart = charts / product
    metadata = chart / "Chart.yaml"
    body = re.sub(r"^(version|appVersion): .*$", rf'\1: "{version}"', metadata.read_text(), flags=re.M)
    metadata.write_text(body)
    image_repo, image_tag = image.rsplit(":", 1)
    values = chart / "values.yaml"
    body, count = re.subn(r"^image:\n  repository: [^\n]+\n  tag: [^\n]+",
                          f'image:\n  repository: {image_repo}\n  tag: "{image_tag}"',
                          values.read_text(), flags=re.M)
    if count != 1:
        raise SystemExit(f"expected one image configuration in {values}")
    values.write_text(body)
PY

bash "$package/deploy/kubernetes/render.sh"
printf 'Simple Hack %s\n\nStart with docs/platforms/simple-hack-standalone.md.\nImage: %s\nSource: %s\n' \
  "$version" "$hack_image" "$commit" > "$package/README.txt"
