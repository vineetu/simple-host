# Simple Host small box on Fly.io: the released server plus Caddy in one image.
#
# On a VPS the small box is three compose containers (Postgres, server, Caddy).
# A Fly volume attaches to one machine, so the server and Caddy, which share
# the sites directory, share one image here. Postgres is a separate Fly app.
# Nothing is compiled: the binary is copied out of the release image, and the
# schema comes from the same release tag.
#
# Upgrade: change SIMPLE_HOST_VERSION in fly.toml and `fly deploy`; the
# release_command applies the new release's migrations before it starts.
ARG SIMPLE_HOST_VERSION=0.7.12
FROM ghcr.io/vineetu/simple-host:${SIMPLE_HOST_VERSION} AS release

FROM caddy:2-alpine
ARG SIMPLE_HOST_VERSION
RUN apk add --no-cache postgresql16-client su-exec \
 && adduser -D -H -u 65532 app
COPY --from=release /usr/local/bin/simple-host /usr/local/bin/simple-host
ADD https://raw.githubusercontent.com/vineetu/simple-host/v${SIMPLE_HOST_VERSION}/db/schema.sql /opt/simple-host/schema.sql
RUN chmod 644 /opt/simple-host/schema.sql
COPY Caddyfile /etc/caddy/Caddyfile
COPY start.sh /usr/local/bin/start.sh
ENV DATA_DIR=/data/sites PORT=8090 BIND_ADDR=127.0.0.1 \
    ANALYTICS_LOG=/data/caddy-logs/access.log XDG_DATA_HOME=/data/caddy XDG_CONFIG_HOME=/tmp/caddy-config
ENTRYPOINT ["/usr/local/bin/start.sh"]
