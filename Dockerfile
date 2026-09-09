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
# The runtime stage cannot run commands, so the data directory is made here and
# copied in. This stage is always native, so nothing is emulated.
RUN mkdir -p /data-empty

# Runtime. distroless/static rather than alpine specifically so this stage runs
# NO commands: an Alpine `apk add` executes on the TARGET architecture, which
# means QEMU emulation for every non-native platform in a multi-arch build.
# distroless/static already ships CA certificates, timezone data and a nonroot
# user, so the whole stage is two COPYs and nothing is ever emulated.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=nonroot:nonroot /data-empty /data/sites
COPY --from=build /out/simple-host /usr/local/bin/simple-host
USER nonroot
ENV DATA_DIR=/data/sites PORT=8090
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/simple-host"]
