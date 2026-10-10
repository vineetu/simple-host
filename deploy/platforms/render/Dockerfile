# Simple Host small box on Render: the released server plus Caddy in one image.
#
# On a VPS the small box is three compose containers (Postgres, server, Caddy).
# A Render disk attaches to one service, so the server and Caddy, which share
# the sites directory, share one image here. Postgres is a Render database.
# Nothing is compiled: the binary is copied out of the release image, and the
# schema comes from the same release tag.
#
# Upgrade: change SIMPLE_HOST_VERSION below and push; the Pre-Deploy Command
# applies the new release's migrations before the new version starts.
ARG SIMPLE_HOST_VERSION=0.7.15
FROM ghcr.io/vineetu/simple-host:${SIMPLE_HOST_VERSION} AS release

FROM caddy:2-alpine
ARG SIMPLE_HOST_VERSION
# Render will not start a program that carries file capabilities
# ("caddy: Operation not permitted"). Caddy has one only to bind ports below
# 1024; here it listens on Render's port (10000), so it is removed.
RUN apk add --no-cache postgresql16-client su-exec libcap-utils \
 && setcap -r /usr/bin/caddy \
 && apk del libcap-utils \
 && adduser -D -H -u 65532 app
COPY --from=release /usr/local/bin/simple-host /usr/local/bin/simple-host
ADD https://raw.githubusercontent.com/vineetu/simple-host/v${SIMPLE_HOST_VERSION}/db/schema.sql /opt/simple-host/schema.sql
RUN chmod 644 /opt/simple-host/schema.sql
COPY Caddyfile /etc/caddy/Caddyfile
COPY start.sh /usr/local/bin/start.sh
ENV DATA_DIR=/data/sites BIND_ADDR=127.0.0.1 \
    ANALYTICS_LOG=/data/caddy-logs/access.log XDG_DATA_HOME=/data/caddy XDG_CONFIG_HOME=/tmp/caddy-config
# CMD, not ENTRYPOINT: Render's Pre-Deploy Command then runs on its own
# (`/usr/local/bin/start.sh release`) instead of being appended to an entrypoint.
CMD ["/usr/local/bin/start.sh"]
