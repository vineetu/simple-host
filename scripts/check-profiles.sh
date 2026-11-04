#!/usr/bin/env bash
# Offline deployment contracts. Optional repo root supports temporary drift fixtures.
set -euo pipefail
cd "${1:-$(dirname "$0")/..}"
python3 - <<'PY'
import json
import re
from pathlib import Path
import yaml

settings = json.loads(Path('docs/advanced/settings.json').read_text())['settings']
registered = {s['name'] for s in settings}
required = {s['name'] for s in settings if s['required']}
errors = []

# These belong to Postgres, Compose, Caddy or the standalone entrypoint, not Go.
nonserver = set('POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB DB_PASSWORD IMAGE '
                'HTTP_PORT HTTPS_PORT SITE_ADDR CONTENT_ADDR ACME_EMAIL '
                'SIMPLE_HACK_IMAGE HTTP_BIND HTTPS_BIND HEALTH_BIND '
                'TLS_MODE ACME_CA CADDY_PROXY_PROTOCOL COMPOSE_PROJECT_NAME'.split())

def read(path):
    return Path(path).read_text()

def names_ok(names, source):
    for name in sorted(set(names) - registered - nonserver):
        errors.append(f'{source}: unregistered setting {name}')

def supplied(names, source):
    for name in sorted(required - set(names)):
        errors.append(f'{source}: missing Required setting {name}')

def block(text, key):
    # Read a YAML block without trying to parse Helm expressions as YAML.
    lines = text.splitlines()
    for i, line in enumerate(lines):
        if line.strip() != key + ':':
            continue
        indent = len(line) - len(line.lstrip())
        body = []
        for line in lines[i + 1:]:
            if not line.strip() or line.lstrip().startswith(('#', '{{')):
                continue
            if len(line) - len(line.lstrip()) <= indent:
                break
            body.append(line)
        return '\n'.join(body)
    return ''

def setting_names(text, key, source):
    names = set(re.findall(r'^\s+([A-Z][A-Z0-9_]*):', block(text, key), re.M))
    if not names:
        errors.append(f'{source}: empty or missing {key} settings block')
    return names

for path in ['compose.yaml', 'deploy/hack/standalone/compose.yaml',
             'deploy/hack/standalone/coolify/compose.yaml']:
    text = read(path)
    config = yaml.safe_load(text)
    if 'app' not in config['services']:
        errors.append(f'{path}: missing app service')
    # Includes interpolation in image names, ports and proxy labels, too.
    names_ok(re.findall(r'\$\{([A-Z][A-Z0-9_]*)', text), path)
    for service, value in config['services'].items():
        env = value.get('environment', {})
        names = set(env) if isinstance(env, dict) else {v.split('=')[0] for v in env}
        names_ok(names, f'{path}:{service}')
        if service in ('app', 'release'):
            supplied(names, f'{path}:{service}')

chart = 'deploy/kubernetes/simple-hack/'
values = yaml.safe_load(read(chart + 'values.yaml'))
# Template keys are literal uppercase names; extraConfig/extraSecrets are dynamic.
config_names = setting_names(read(chart + 'templates/configmap.yaml'), 'data', 'Helm ConfigMap')
secret_names = setting_names(read(chart + 'templates/secret.yaml'), 'stringData', 'Helm generated Secret')
deployment = read(chart + 'templates/deployment.yaml')
env_from = block(deployment, 'envFrom')
for ref, helper in [('configMapRef', 'configName'), ('secretRef', 'secretName')]:
    if not re.search(r'-\s*' + ref + r':\s*\n\s+name:\s*{{\s*include "hack\.' + helper + r'" \.\s*}}', env_from):
        errors.append(f'Helm deployment: missing envFrom {ref} for hack.{helper}')
config_names.update(values.get('extraConfig', {}))
secret_names.update(values.get('extraSecrets', {}))
names_ok(config_names | secret_names, 'Helm ConfigMap/Secret/values')
# Default wiring uses the generated Secret (operator fills database.dsn).
# The conditional database.existingSecret env entry is not a fallback here.
supplied(config_names | secret_names, 'Helm default ConfigMap + generated Secret')
names_ok(re.findall(r'- name: ([A-Z][A-Z0-9_]*)', deployment), 'Helm deployment')
bundle = list(yaml.safe_load_all(read('deploy/kubernetes/manifests/simple-hack.yaml')))
manifest_names = set()
manifest_names.update(re.findall(r'- name: ([A-Z][A-Z0-9_]*)', read('deploy/kubernetes/manifests/simple-hack.yaml')))
for resource in bundle:
    if resource and resource['kind'] in ('ConfigMap', 'Secret'):
        manifest_names.update(resource.get('data', {}))
        manifest_names.update(resource.get('stringData', {}))
secret = yaml.safe_load(read('deploy/kubernetes/manifests/secret.example.yaml'))
manifest_names.update(secret['stringData'])
names_ok(manifest_names, 'Kubernetes manifests and Secret example')
supplied(manifest_names, 'Kubernetes manifests and Secret example')

doc = read('docs/operations/deployment-profiles.md')
# Ignore comments and compare with the FIRST app-starting up, including an
# explicit `up ... app`. The earlier database-only up is intentionally allowed.
installer = '\n'.join(line.split('#', 1)[0] for line in read('deploy/install/install.sh').splitlines())
migrate = re.search(r'\bdocker compose run --rm -T app migrate\b', installer)
app_ups = [m for m in re.finditer(r'\bdocker compose up\b[^\n]*', installer)
           if m.group().split()[-1] != 'db']
checks = {
    'Kubernetes owns startup migrations': values['migrateOnStart'] is True
        and 'MIGRATE_ON_START: {{ ternary "true" "false" .Values.migrateOnStart | quote }}' in read(chart + 'templates/configmap.yaml')
        and any(r and r['kind'] == 'ConfigMap' and r['data'].get('MIGRATE_ON_START') == 'true' for r in bundle)
        and 'Kubernetes: the server migrates before listening.' in doc,
    'Compose installer migrates before starting app': bool(migrate and app_ups and migrate.start() < app_ups[0].start())
        and 'Compose: the installer migrates before starting the new app.' in doc,
    'Hosted unit leaves startup migration off': 'MIGRATE_ON_START' not in read('deploy/prod/simple-host.service')
        and 'Hosted: the operator owns migrations; startup migration is off.' in doc,
}
# Unit files expose only EnvironmentFile, not its keys. Check the documented
# required contract without opening /etc/*.env; operator values remain unchecked.
hosted_doc = doc.split('## Hosted systemd', 1)[1].split('## Docker Compose', 1)[0]
supplied(set(re.findall(r'`([A-Z][A-Z0-9_]*)`', hosted_doc)), 'Hosted documented env-file contract')
for label, ok in checks.items():
    if not ok:
        errors.append(label)
if errors:
    print('\n'.join('FAIL: ' + error for error in errors))
    raise SystemExit(1)
print('deployment profiles: registered names, Required settings and migration owners agree')
PY
