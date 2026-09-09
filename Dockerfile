# Build stage. Pinned to the BUILD platform so the Go toolchain always runs
# natively and cross-compiles to the target. No cgo in this module, so a
# multi-arch image needs no emulation at all.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/simple-host ./cmd/server

# Runtime. No toolchain, no source, no shell tooling beyond what the binary
# needs to make outbound TLS calls and resolve timezones.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -u 10001 app \
 && mkdir -p /data/sites && chown -R app:app /data
COPY --from=build /out/simple-host /usr/local/bin/simple-host
USER app
ENV DATA_DIR=/data/sites PORT=8090
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/simple-host"]
