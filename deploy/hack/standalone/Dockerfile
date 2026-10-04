# Build from the same tagged checkout as SIMPLE_HOST_IMAGE.
ARG SIMPLE_HOST_IMAGE=ghcr.io/vineetu/simple-host:hack-0.8.3
FROM ${SIMPLE_HOST_IMAGE} AS server
FROM caddy:2.11.4-alpine
RUN apk add --no-cache postgresql16-client su-exec \
 && adduser -D -H -u 65532 app
COPY --from=server /usr/local/bin/simple-host /usr/local/bin/simple-host
COPY db/schema.sql /opt/simple-hack/schema.sql
COPY deploy/hack/standalone/Caddyfile /etc/caddy/Caddyfile
COPY deploy/hack/standalone/start.sh /usr/local/bin/start-hack
RUN chmod 755 /usr/local/bin/start-hack
ENV DATA_DIR=/data/sites BIND_ADDR=127.0.0.1 PORT=8090 \
    ANALYTICS_LOG=/data/logs/access.log XDG_DATA_HOME=/data/caddy \
    XDG_CONFIG_HOME=/data/caddy-config
EXPOSE 8080 8081 8443
ENTRYPOINT ["/usr/local/bin/start-hack"]
