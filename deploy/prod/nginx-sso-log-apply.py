#!/usr/bin/env python3
"""Install safe access logging. Never reads the content-host configuration."""
import datetime
import pathlib
import re
import shutil
import subprocess
import sys

root = pathlib.Path('/etc/nginx')
source = pathlib.Path(__file__).with_name('nginx-sso-log.conf')
dest = root / 'snippets/simple-host-safe-log.conf'
paths = [root / 'nginx.conf']
# These are the application and hosted-site proxy configs, not arbitrary services.
for p in (root / 'sites-enabled').iterdir():
    if p.name == 'sites-content-host':
        continue
    if p.name in {'simple-host', 'simple-hack', 'simple-host-site', 'vineetsriram.com', 'sf-fog.today'} or p.name.startswith(('simple-host-domain-', 'simple-host-family-')):
        resolved = p.resolve()
        if resolved.name != "sites-content-host":
            paths.append(resolved)
changes = {dest: source.read_bytes()}
for path in dict.fromkeys(paths):
    old = path.read_text()
    new = re.sub(r'(access_log\s+(?!off(?:\s|;))[^;\s]+)(?:\s+combined)?\s*;', r'\1 shsafe;', old)
    if path == root / 'nginx.conf' and 'include /etc/nginx/snippets/simple-host-safe-log.conf;' not in new:
        new = re.sub(r'(\s*access_log\s)', '\n\tinclude /etc/nginx/snippets/simple-host-safe-log.conf;\n\\1', new, count=1)
    if new != old:
        changes[path] = new.encode()
if '--apply' not in sys.argv:
    print('Safe-log files to install:', len(changes))
    sys.exit(0)
stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%d-%H%M%S')
backups = {}
try:
    for path, body in changes.items():
        if path.exists():
            backup = pathlib.Path('/var/backups/simple-host-sso-nginx') / stamp / path.relative_to(root)
            backup.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, backup)
            backups[path] = backup
        else:
            backups[path] = None
        path.write_bytes(body)
    subprocess.run(['/usr/sbin/nginx', '-t'], check=True, capture_output=True)
    subprocess.run(['systemctl', 'reload', 'nginx'], check=True, capture_output=True)
except Exception:
    for path, backup in backups.items():
        if backup:
            shutil.copy2(backup, path)
        else:
            path.unlink(missing_ok=True)
    print('Safe-log install failed; restored configuration.', file=sys.stderr)
    sys.exit(1)
print('Safe access logging installed; nginx configuration checked and reloaded.')
