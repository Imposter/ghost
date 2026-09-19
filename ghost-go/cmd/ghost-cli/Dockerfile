# syntax=docker/dockerfile:1
# Build from the repository root, like the ghost-server image:
#   docker build -f ghost-go/cmd/ghost-cli/Dockerfile -t ghost-cli .
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src/ghost-go
COPY ghost-go/go.mod ghost-go/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY ghost-go ./
ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go build -trimpath -ldflags "-s -w" -o /out/ghost-cli ./cmd/ghost-cli

# Alpine, with curl and jq for scripts that drive the control API.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl jq \
 && addgroup -S -g 65532 ghost \
 && adduser -S -D -H -u 65532 -G ghost ghost \
 && mkdir -p /var/lib/ghost \
 && chown ghost:ghost /var/lib/ghost
COPY --from=build /out/ghost-cli /usr/local/bin/ghost-cli
# Keys and credentials default to ghost-keys.json and ghost-creds.json here.
WORKDIR /var/lib/ghost
VOLUME ["/var/lib/ghost"]
USER ghost:ghost
ENTRYPOINT ["/usr/local/bin/ghost-cli"]
