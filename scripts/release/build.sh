#!/usr/bin/env bash
# Build release files locally. Publication is owned by release.yml.
set -euo pipefail
cd "$(dirname "$0")/../.."

command=${1:-}
tag=${2:-}
if [[ ! $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?$ ]]; then
  echo 'usage: build.sh {metadata TAG | {artifacts,install-assets,verify-binaries,images,verify-images} TAG DIRECTORY}' >&2
  exit 2
fi
version=${tag#v}
commit=$(git rev-parse HEAD)
repository=${GITHUB_REPOSITORY:-vineetu/simple-host}
repository=$(printf '%s' "$repository" | tr '[:upper:]' '[:lower:]')
host_repo=ghcr.io/$repository
hack_repo=ghcr.io/${repository%%/*}/simple-hack
stable=true
if [[ $tag == v*-rc.* ]]; then
  stable=false
  host_repo+=/dry-run/simple-host
  hack_repo=ghcr.io/$repository/dry-run/simple-hack
fi
host_image=$host_repo:$version
hack_base_image=$host_repo:hack-$version
hack_image=$hack_repo:$version

if [[ $command == metadata && $# == 2 ]]; then
  printf '%s\n' "stable=$stable" "commit=$commit" "host_image=$host_image" \
    "hack_base_image=$hack_base_image" "hack_image=$hack_image" "host_latest=$host_repo:latest"
  exit
fi
[[ $# == 3 ]] || exit 2
out=$(mkdir -p "$3" && cd "$3" && pwd)

verify_version() {
  local program=$1 output
  shift
  output=$("$@")
  if [[ ${output%%$'\n'*} != "simple-host $tag (commit $commit)" ]]; then
    printf 'FAIL: %s does not report %s at %s\n%s\n' "$program" "$tag" "$commit" "$output" >&2
    return 1
  fi
  printf '%s: %s\n' "$program" "${output%%$'\n'*}"
}

verify_native_binaries() {
  local os arch product binary
  os=$(go env GOHOSTOS)
  arch=$(go env GOHOSTARCH)
  for product in simple-host simple-hack; do
    binary=$out/${product}_${os}_${arch}
    chmod +x "$binary"
    verify_version "$product download ($os/$arch)" "$binary" version
  done
}

build_install_assets() {
  local package=simple-hack-$version product
  bash scripts/release/package-hack.sh "$stage/$package" "$tag" "$commit" "$repository" \
    "$host_image" "$hack_base_image" "$hack_image"
  for product in simple-host simple-hack; do
    helm package "$stage/$package/deploy/kubernetes/$product" -d "$out"
  done
  tar -C "$stage" -czf "$out/$package.tar.gz" "$package"
  (cd "$stage" && zip -qr "$out/$package.zip" "$package")
  python3 - "$out" "$package" "$version" "$host_image" "$hack_base_image" "$hack_image" <<'PY'
import json
from pathlib import Path
import shlex
import subprocess
import sys
from tarfile import open as open_tar
from zipfile import ZipFile

import yaml

out, package, version, host_image, hack_base_image, hack_image = sys.argv[1:]
base = Path(out, package)
schema = (Path('host/db/schema.sql').read_bytes() + b'\n' +
          Path('products/hack/db/schema.sql').read_bytes())

# Run the documented command against Deployment fixtures and inspect the Pod override.
# The kubectl stand-in reads only the supplied Deployment and records the run arguments.
def check_migration(command, product, image):
    kubectl = r'''
kubectl() {
    case "$3" in
        get) [[ "$*" == "-n $product get deployment $product -o json" ]] && cat ;;
        run) printf '%s\0' "$@" ;;
        *) return 1 ;;
    esac
}
'''
    for database_ref in (None, {'name': 'custom-database', 'key': 'connection'}):
        pod_security = {'runAsNonRoot': True, 'runAsUser': 65532, 'seccompProfile': {'type': 'RuntimeDefault'}}
        container_security = {'allowPrivilegeEscalation': False, 'readOnlyRootFilesystem': True,
                              'capabilities': {'drop': ['ALL']}}
        container = {'name': product, 'securityContext': container_security,
                     'envFrom': [{'configMapRef': {'name': 'config'}},
                                 {'secretRef': {'name': 'install-secrets'}}]}
        pod = {'containers': [container], 'securityContext': pod_security}
        if database_ref:
            container['env'] = [{'name': 'DB_DSN', 'valueFrom': {'secretKeyRef': database_ref}}]
            pod['imagePullSecrets'] = [{'name': 'private-registry'}, {'name': 'second-registry'}]
        deployment = {'spec': {'template': {'spec': pod}}}
        args = subprocess.check_output(['bash', '-ec', 'product=$1\n' + kubectl + command, 'bash', product],
                                       input=json.dumps(deployment), text=True).split('\0')[:-1]
        patch, = (arg for arg in args if arg.startswith('--overrides='))
        args.remove(patch)
        if args != ['-n', product, 'run', 'migrate', '--rm', '-it', '--restart=Never', '--image', image,
                    '--override-type=strategic', '--', 'migrate', '-init', '-record-product']:
            raise SystemExit(f'Incorrect packaged migration command for {product}')
        override = json.loads(patch.removeprefix('--overrides='))
        ref = database_ref or {'name': 'install-secrets', 'key': 'DB_DSN'}
        expected = {'name': 'migrate', 'securityContext': container_security,
                    'env': [{'name': 'DB_DSN', 'valueFrom': {'secretKeyRef': ref}}]}
        expected_spec = {'containers': [expected], 'securityContext': pod_security,
                         'automountServiceAccountToken': False, 'imagePullSecrets': pod.get('imagePullSecrets', [])}
        if override['spec'] != expected_spec:
            raise SystemExit(f'Incorrect security settings, pull secrets or database Secret in packaged {product} migration Pod')


with open_tar(f'{base}.tar.gz') as tar, ZipFile(f'{base}.zip') as zip_file:
    for read in (lambda name: tar.extractfile(name).read(), zip_file.read):
        if read(f'{package}/db/schema.sql') != schema:
            raise SystemExit('Packaged db/schema.sql differs from the combined Host and Hack schema')
        guide = read(f'{package}/docs/kubernetes.md').decode()
        intro = guide.split('\n## ', 1)[0]
        renderer = 'deploy/kubernetes/render.sh'
        if (f'`v{version}`' not in intro or 'chart defaults' not in intro or
                f'`bash {renderer}`' not in intro or 'deploy/shared/' in intro or 'locally built' in intro):
            raise SystemExit('Packaged Helm setup must use the tagged chart defaults and bundled renderer')
        read(f'{package}/{renderer}')
        host_section = guide.split('## Platform alone\n', 1)[1].split('\n## ', 1)[0]
        install_section = guide.split('## Install\n', 1)[1].split('\n## ', 1)[0]
        upgrade_section = guide.split('## Upgrades\n', 1)[1].split('\n## ', 1)[0]
        for section in (host_section, upgrade_section):
            if '](upgrades.md#kubernetes)' not in section:
                raise SystemExit('Both packaged Helm upgrade sections must link to the paired procedure')
        if 'helm upgrade simple-host' in host_section or '--set image.tag=' in upgrade_section:
            raise SystemExit('Packaged Helm instructions must use the paired upgrade and chart defaults')
        init = 'migrate -init -record-product'
        if (init in upgrade_section or 'only for an empty database' not in install_section
                or '](upgrades.md#kubernetes)' not in install_section):
            raise SystemExit('Packaged manual initialization must be limited to fresh installs with an empty database')
        for label, product, image in (('Simple Host', 'simple-host', host_image),
                                      ('Simple Hack', 'simple-hack', hack_base_image)):
            prefix = f'**{label}**\n\n```sh\n'
            command = install_section.split(prefix, 1)[1].split('\n```', 1)[0]
            check_migration(command, product, image)
            if f'IMAGE={image} bash deploy/kubernetes/tests/kind-check.sh {product}' not in guide:
                raise SystemExit(f'Packaged {label} kind test must select {image}')
        if f'/tree/v{version})' not in guide.split('## Test the charts\n', 1)[1]:
            raise SystemExit('Packaged chart tests must run from the tagged checkout')
        guide = read(f'{package}/docs/upgrades.md').decode()
        hack_helper = 'deploy/hack/standalone/upgrade.sh'
        hack_command, = (shlex.split(line) for line in guide.splitlines() if line.startswith(f'sudo bash {hack_helper} '))
        if hack_command != ['sudo', 'bash', hack_helper, '--to', version, '--image', hack_image]:
            raise SystemExit('Packaged Hack upgrade must use the tagged version and image')
        read(f'{package}/{hack_helper}')
        if '-rc.' in version:
            ocean = read(f'{package}/docs/platforms/simple-hack-digitalocean.md').decode()
            if ('sudo docker login ghcr.io' not in ocean or
                '](simple-hack-standalone.md#install)' not in ocean):
                raise SystemExit('DigitalOcean RC rehearsals must use the standalone installer on an authenticated droplet')
            installer = 'deploy/install/install.sh'
            install, = (shlex.split(line) for line in guide.splitlines() if line.startswith(f'sudo bash {installer} '))
            if (install != ['sudo', 'bash', installer, '--ref', f'v{version}', '--image', host_image] or
                read(f'{package}/{installer}') != Path(installer).read_bytes()):
                raise SystemExit('Fresh Host installs must use the tagged installer, source ref and dry-run image')
            command, = (shlex.split(line) for line in guide.splitlines()
                        if line.startswith('sudo bash deploy/shared/upgrade.sh '))
            if ('unpacked RC archive' not in guide or
                command[command.index('--image') + 1] != host_image or
                command[command.index('--to') + 1] != version):
                raise SystemExit('Packaged RC guide must use the candidate Host image and packaged helper')
            for path in (command[2], command[command.index('--compose') + 1],
                         command[command.index('--caddyfile') + 1]):
                read(f'{package}/{path}')
print('Verified schema and upgrade instructions in both installation archives')

for product, expected in (('simple-host', host_image), ('simple-hack', hack_base_image)):
    chart = Path(out, f'{product}-{version}.tgz')
    docs = yaml.safe_load_all(subprocess.check_output([
        'helm', 'template', product, str(chart), '--set', 'host=fixture.example.test',
        '--set', 'secrets.existingSecret=fixture',
    ], text=True))
    deployment, = (doc for doc in docs if doc and doc['kind'] == 'Deployment')
    pod = deployment['spec']['template']['spec']
    for containers, name in ((pod['containers'], product), (pod['initContainers'], 'migrate')):
        container, = (entry for entry in containers if entry['name'] == name)
        if container['image'] != expected:
            raise SystemExit(f'{chart.name}: {name} must use {expected}, found {container["image"]}')
    print(f'{chart.name}: verified serving and migration image {expected}')
PY
}

if [[ $command == artifacts || $command == install-assets ]]; then
  stage=$(mktemp -d)
  trap 'rm -rf "$stage"' EXIT
fi

case $command in
  artifacts)
    buildinfo=github.com/vsriram/simple-host/host/buildinfo
    build_time=$(git show -s --format=%ct HEAD)
    build_time=$(date -u -d "@$build_time" +%Y-%m-%dT%H:%M:%SZ)
    ldflags="-s -w -X $buildinfo.Version=$tag -X $buildinfo.Commit=$commit -X $buildinfo.BuildTime=$build_time"
    for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
      os=${pair%/*}
      arch=${pair#*/}
      for product in simple-host simple-hack; do
        binary=$out/${product}_${os}_${arch}
        CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags="$ldflags" -o "$binary" "./cmd/$product"
        settings=$(go version -m "$binary")
        for setting in "-trimpath=true" "GOOS=$os" "GOARCH=$arch"; do
          if ! grep -Fxq $'\tbuild\t'"$setting" <<< "$settings"; then
            printf 'FAIL: %s is missing expected build setting %s\n' "$binary" "$setting" >&2
            exit 1
          fi
        done
        printf 'Verified build settings for %s (%s/%s)\n' "$product" "$os" "$arch"
      done
    done
    verify_native_binaries
    build_install_assets

    # Both Simple Host plugins take their skills from the one source the
    # server serves.
    mkdir -p "$stage/scripts" "$stage/host-claude/skills" "$stage/hack-toolkit" "$stage/plugins" "$stage/skills"
    cp scripts/build-openai-plugin.sh "$stage/scripts/"
    cp -a plugins/openai "$stage/plugins/"
    git archive --format=tar HEAD:host/webassets/simple-host-website/skills | tar -x -C "$stage/skills"
    git archive --format=tar HEAD:plugins/simple-host | tar -x -C "$stage/host-claude"
    cp -a "$stage/skills/." "$stage/host-claude/skills/"
    kit=products/hack/webassets/hack-toolkit
    cp -a "$kit"/{build.py,metadata,skills,CLAUDE-README.md} "$stage/hack-toolkit/"
    python3 - "$version" "$stage/plugins/openai/plugin.json" "$stage/host-claude/.claude-plugin/plugin.json" \
      "$stage/hack-toolkit/metadata/"{listing,claude-plugin}.json <<'PY'
import json
from pathlib import Path
import sys

version, *manifests = sys.argv[1:]
for name in manifests:
    path = Path(name)
    manifest = json.loads(path.read_text())
    manifest['version'] = version
    path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + '\n')
PY
    SKILLS="$stage/skills" FALLBACK=1 bash "$stage/scripts/build-openai-plugin.sh"
    mv "$stage/dist/simple-host-openai-plugin.zip" "$out/simple-host-openai-plugin-$version.zip"
    mv "$stage/dist/simple-host-openai-skills.zip" "$out/simple-host-openai-skills-$version.zip"
    mv "$stage/dist/website-deploy-toolkit-$version.zip" "$out/"
    (cd "$stage/host-claude" && zip -qr "$out/simple-host-claude-$version.zip" .)
    python3 - "$out/simple-host-claude-$version.zip" <<'PY'
import sys
from zipfile import ZipFile
names = set(ZipFile(sys.argv[1]).namelist())
for need in ['.claude-plugin/plugin.json', '.mcp.json'] + [f'skills/{s}/SKILL.md' for s in ('website-deploy', 'website-deploy-builder', 'connect-domain')]:
    if need not in names:
        raise SystemExit(f'{sys.argv[1]}: missing {need}')
PY

    python3 "$stage/hack-toolkit/build.py"
    for bundle in openai claude skills-only skills; do
      mv "$stage/hack-toolkit/site/downloads/simple-hack-$bundle-$version.zip" "$out/"
    done
    python3 - "$out" "$version" <<'PY'
import json
from pathlib import Path
import sys
from zipfile import ZipFile

out, version = sys.argv[1:]
for bundle, member in (
    ('simple-host-openai-plugin', 'plugin.json'),
    ('simple-host-claude', '.claude-plugin/plugin.json'),
    ('website-deploy-toolkit', 'plugin.json'),
    ('simple-hack-openai', 'simple-hack/plugin.json'),
    ('simple-hack-skills-only', 'simple-hack/plugin.json'),
    ('simple-hack-claude', 'simple-hack/.claude-plugin/plugin.json'),
):
    path = Path(out, f'{bundle}-{version}.zip')
    with ZipFile(path) as archive:
        actual = json.loads(archive.read(member))['version']
    if actual != version:
        raise SystemExit(f'{path.name}: {member} reports {actual}, expected {version}')
    print(f'{path.name}: verified manifest version {actual}')
PY
    (cd "$out" && sha256sum simple-* website-deploy-toolkit-*.zip > SHA256SUMS)
    ;;
  install-assets)
    build_install_assets
    ;;
  verify-binaries)
    verify_native_binaries
    ;;
  images)
    # Bake passes the locally built root Hack target straight to the wrapper.
    # Export all three images before publishing any of them.
    RELEASE_TAG=$tag RELEASE_COMMIT=$commit RELEASE_IMAGE_DIR=$out \
      docker buildx bake --allow "fs.write=$out" -f scripts/release/images.hcl
    ;;
  verify-images)
    IFS=, read -r -a platforms <<< "${RELEASE_PLATFORMS:-linux/amd64,linux/arm64}"
    for product in host hack standalone; do
      for platform in "${platforms[@]}"; do
        os=${platform%/*}
        arch=${platform#*/}
        # Select only this platform for the local daemon. Publication copies
        # the original index with both platforms and their attestations.
        image=release-verify-$product:$$-$arch
        trap 'docker image rm "$image" >/dev/null 2>&1 || true' EXIT
        skopeo --override-os "$os" --override-arch "$arch" copy "oci-archive:$out/$product.tar" "docker-daemon:$image"
        verify_version "$product image ($platform)" docker run --rm --network none --platform "$platform" "$image" version
        docker image rm "$image" >/dev/null
        trap - EXIT
      done
    done
    ;;
  *)
    echo "Unknown release build command: $command" >&2
    exit 2
    ;;
esac
