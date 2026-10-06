# Simple Host small box on Coolify: the released server plus Caddy in one image.
#
# On a VPS the small box is three compose containers (Postgres, server, Caddy).
# Here the server and Caddy, which share the sites directory, share one image
# and one volume. Postgres is its own container.
# Nothing is compiled: the binary is copied out of the release image, and the
# schema comes from the same release tag.
#
# Upgrade: set SIMPLE_HOST_VERSION (compose build arg) and redeploy; the release
# service applies the new release's migrations before the new version starts.
ARG SIMPLE_HOST_VERSION=0.7.10
FROM ghcr.io/vineetu/simple-host:${SIMPLE_HOST_VERSION} AS release

FROM caddy:2-alpine
ARG SIMPLE_HOST_VERSION
# Caddy carries a file capability only to bind ports below 1024; it listens
# on 8080 here and runs as a normal user, so it is removed.
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
# CMD, not ENTRYPOINT: the release service's command then replaces it
# (`/usr/local/bin/start.sh release`) instead of being appended to an entrypoint.
CMD ["/usr/local/bin/start.sh"]
