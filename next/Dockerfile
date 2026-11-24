# The Simple Host service as one small image, for a self-hosted install
# (web/selfhost/compose.yaml). Build from this folder: docker build -t simple-host .
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /simple-host ./cmd/simple-host-next

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /simple-host /simple-host
USER nonroot
EXPOSE 8091
ENTRYPOINT ["/simple-host"]
