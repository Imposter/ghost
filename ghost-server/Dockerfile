# syntax=docker/dockerfile:1
# Build from the repository root so the ghost-go module (a replace target) is
# in the context:
#   docker build -f ghost-server/Dockerfile -t ghost-server .
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY ghost-go/go.mod ghost-go/go.sum ./ghost-go/
COPY ghost-server/go.mod ghost-server/go.sum ./ghost-server/
RUN --mount=type=cache,target=/go/pkg/mod \
    cd ghost-server && go mod download
COPY ghost-go ./ghost-go
COPY ghost-server ./ghost-server
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd ghost-server && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/ghost-server ./cmd/ghost-server

# Alpine rather than distroless so the healthcheck has a shell and wget.
FROM alpine:3.24
RUN apk add --no-cache ca-certificates \
 && addgroup -S -g 65532 ghost \
 && adduser -S -D -H -u 65532 -G ghost ghost \
 && mkdir -p /var/lib/ghost \
 && chown ghost:ghost /var/lib/ghost
COPY --from=build /out/ghost-server /usr/local/bin/ghost-server
WORKDIR /var/lib/ghost
ENV GHOST_LISTEN=:8080 \
    GHOST_DB_DRIVER=sqlite \
    GHOST_DB_DSN=/var/lib/ghost/ghost-server.db
EXPOSE 8080
VOLUME ["/var/lib/ghost"]
USER ghost:ghost
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${GHOST_LISTEN##*:}/healthz" || exit 1
ENTRYPOINT ["/usr/local/bin/ghost-server"]
