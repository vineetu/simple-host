#!/usr/bin/env bash
# Check an existing Host recording, or record this checkout on disposable PG.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
text_dir=${1:-${GOLDEN_TEXT_DIR:-}}
if [[ $# -gt 1 ]]; then
    echo 'usage: check-public-text.sh [GOLDEN_RUN/text/host]' >&2
    exit 2
fi
if [[ -n $text_dir ]]; then
    text_dir=$(cd "$text_dir" && pwd)
else
    work=$(mktemp -d)
    container_id=""
    cleanup() {
        if [[ -n $container_id ]]; then
            docker rm -fv "$container_id" >/dev/null 2>&1 \
                || echo "warning: failed to remove disposable public-text container $container_id" >&2
        fi
        rm -rf "$work"
    }
    trap cleanup EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM

    base=${TEST_PG_URL:-}
    if [[ -z $base ]]; then
        if ! command -v docker >/dev/null 2>&1; then
            echo 'FAIL: no TEST_PG_URL and no docker available -- set TEST_PG_URL or supply the Host recording text folder' >&2
            exit 1
        fi
        echo '== starting throwaway postgres:16-alpine via Docker =='
        pgpass=$(openssl rand -hex 16)
        # Create separately so cleanup owns the container even if start fails.
        container_id=$(docker create -e POSTGRES_PASSWORD="$pgpass" \
            -p 127.0.0.1::5432 postgres:16-alpine)
        docker start "$container_id" >/dev/null
        mapping=$(docker port "$container_id" 5432/tcp | head -1)
        port=${mapping##*:}
        if [[ -z $port ]]; then
            echo 'FAIL: could not determine the disposable Postgres port' >&2
            exit 1
        fi
        base="postgres://postgres:${pgpass}@127.0.0.1:${port}"
        # Probe TCP: the image's temporary bootstrap server is not ready yet.
        ready=0
        for ((attempt = 0; attempt < 60; attempt++)); do
            if psql "${base}/postgres?sslmode=disable" -v ON_ERROR_STOP=1 -XAtc 'select 1' >/dev/null 2>&1; then
                ready=1
                break
            fi
            sleep 1
        done
        if [[ $ready != 1 ]]; then
            echo 'FAIL: disposable Postgres did not become ready' >&2
            exit 1
        fi
    fi
    cd "$root"
    go build -o "$work/" ./cmd/simple-host ./cmd/simple-hack
    GOLDEN_PG_URL="$base" node scripts/golden/run.mjs --host "$work/simple-host" --hack "$work/simple-hack" --out "$work/recording" --scenario base,hosted-pages
    text_dir=$work/recording/text/host
fi
cd "$root"
go run ./scripts/split/boundary -mode public-text -text "$text_dir"
