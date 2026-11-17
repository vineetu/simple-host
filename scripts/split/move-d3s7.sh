#!/usr/bin/env bash
# D3-S7: the one rename of the platform to host/ (split track, section 4), with
# the reviewed text inputs. Re-running it changes nothing. An old branch made
# before D3-S7 is repaired by running this on the branch, committing, and then
# rebasing onto main (see the coordination file). Extra arguments go to move.sh
# (for example --dry-run).
#
# Text inputs beyond move.sh's defaults: Go comments and root-relative strings,
# Markdown, served pages and their sources, deployment tests that read the tree.
# Left alone on purpose: history (docs/history, CHANGELOG.md, PARITY.md, the
# golden approvals), generated files (docs/generated, the built docs site,
# rebuilt afterwards; the MCP app bundles and their generator, whose output
# must not change), SQL migrations and their historical fixtures, deploy/prod
# (its "host/" strings are URL paths), two tests that name paths inside old
# commits (git show REV:PATH), and the platform Dockerfiles with
# their docs, which load the schema from a pinned release tag.
set -euo pipefail
here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
exec bash "$here/move.sh" --map "$here/pathmap-d3s7.txt" \
  --text '*.go' --text '*.md' --text 'web/*' --text '*schema.sql' \
  --text ':(glob)**/webassets/static/**' --text ':(glob)products/hack/webassets/**' \
  --text 'deploy/kubernetes/tests/kind-check.sh' --text 'deploy/install/product_init_test.py' \
  --text 'deploy/digitalocean/droplet/test/pins_test.sh' --text 'deploy/digitalocean/droplet/README.md' \
  --text 'docs/site/astro.config.mjs' \
  --text ':(exclude)docs/history/**' --text ':(exclude)CHANGELOG.md' --text ':(exclude)PARITY.md' \
  --text ':(exclude)docs/generated/**' --text ':(exclude,glob)**/docs-site/**' \
  --text ':(exclude)docs/platforms/**' --text ':(exclude,glob)**/migrations/testdata/**' \
  --text ':(exclude)scripts/golden/approved/**' --text ':(exclude)products/hack/mcp/apps/**' \
  --text ':(exclude)products/hack/servertest/hack_chrome_test.go' --text ':(exclude)products/hack/hackhttp/hack_leave_test.go' \
  "$@"
