#!/usr/bin/env bash
set -euo pipefail
if ! grep -qF '# >>> simple-hack setup >>>' /root/.bashrc; then
  cat >> /root/.bashrc <<'EOF'
# >>> simple-hack setup >>>
if [ -t 0 ] && [ -t 1 ] && [ -x /opt/simple-hack-setup/digitalocean/first-login.sh ]; then
  /opt/simple-hack-setup/digitalocean/first-login.sh
fi
# <<< simple-hack setup <<<
EOF
fi
