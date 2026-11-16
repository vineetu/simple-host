# SIMPLE_HOST_IMAGE is required until a release includes the split programs.
# It must be the root Dockerfile's hack target from this same checkout:
# docker build --target hack -t simple-hack-base:local .
# docker build --build-arg SIMPLE_HOST_IMAGE=simple-hack-base:local -f deploy/hack/standalone/Dockerfile -t simple-hack:local .
ARG SIMPLE_HOST_IMAGE
FROM ${SIMPLE_HOST_IMAGE} AS server
FROM caddy:2.11.4-alpine
RUN apk add --no-cache su-exec \
 && adduser -D -H -u 65532 app
COPY --from=server /usr/local/bin/simple-hack /usr/local/bin/simple-hack
# Transitional alias for existing launch recipes, for this release only.
RUN ln -s simple-hack /usr/local/bin/simple-host
COPY deploy/hack/standalone/Caddyfile /etc/caddy/Caddyfile
COPY deploy/hack/standalone/start.sh /usr/local/bin/start-hack
RUN chmod 755 /usr/local/bin/start-hack
ENV DATA_DIR=/data/sites BIND_ADDR=127.0.0.1 PORT=8090 \
    ANALYTICS_LOG=/data/logs/access.log XDG_DATA_HOME=/data/caddy \
    XDG_CONFIG_HOME=/data/caddy-config
EXPOSE 8080 8081 8443
ENTRYPOINT ["/usr/local/bin/start-hack"]
