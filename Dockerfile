# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/openforge .

# The built-in builder shells out to a container runtime. The runtime image
# ships the Docker CLI and the lightweight Podman remote client; remote
# operation is selected via DOCKER_HOST / CONTAINER_HOST.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates docker-cli podman-remote
COPY --from=build /out/openforge /usr/local/bin/openforge
ENV OPENFORGE_PUBLIC_PATH=/public
# container_engine defaults to "auto" (Podman preferred, Docker fallback).
# Point the engine at a reachable service, e.g.:
#   -e CONTAINER_HOST=unix:///run/podman/podman.sock
#   -e DOCKER_HOST=unix:///var/run/docker.sock
VOLUME ["/public"]
EXPOSE 8080
ENTRYPOINT ["openforge"]
