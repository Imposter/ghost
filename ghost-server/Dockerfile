# syntax=docker/dockerfile:1
# Build from the repository root so the ghost-go module (a replace target) is
# in the context:
#   docker build -f ghost-server/Dockerfile -t ghost-server .
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY ghost-go/go.mod ghost-go/go.sum ./ghost-go/
COPY ghost-server/go.mod ghost-server/go.sum ./ghost-server/
RUN cd ghost-server && go mod download
COPY ghost-go ./ghost-go
COPY ghost-server ./ghost-server
ARG VERSION=dev
RUN cd ghost-server && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/ghost-server ./cmd/ghost-server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ghost-server /usr/local/bin/ghost-server
WORKDIR /var/lib/ghost
ENV GHOST_LISTEN=:8080 \
    GHOST_DB_DRIVER=sqlite \
    GHOST_DB_DSN=/var/lib/ghost/ghost-server.db
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/ghost-server"]
