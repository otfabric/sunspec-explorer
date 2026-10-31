# syntax=docker/dockerfile:1
#
# Container image of sunspec-explorerd, the headless daemon.
#
#   docker build -f packaging/Dockerfile -t sunspec-explorerd .
#   docker run --rm --name explorer -p 127.0.0.1:8765:8765 -v explorer-data:/data sunspec-explorerd
#   docker exec explorer sunspec-explorerd url      # the URL to open, token included
#
# Publish the port on 127.0.0.1 only (as above): inside the container the
# daemon has to listen on every address, and the listener is plain HTTP.
# See docs/REMOTE-USE.md, "In a container".

# The build stages run on the build machine's architecture and cross-compile:
# the web UI is architecture-independent and the daemon is pure Go.
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:fe4f6d74e20370acb04ad889077d14d0f64a9c8ea6a42bc60cddf1e844699914 AS ui
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build:web

FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/sunspec-explorerd cmd/sunspec-explorerd
COPY internal internal
COPY frontend/embed.go frontend/embed.go
COPY --from=ui /src/frontend/dist/web frontend/dist/web
# The desktop build of the UI is embedded by the same package but not linked
# into the daemon: a placeholder satisfies the compiler.
RUN mkdir -p frontend/dist/wails && touch frontend/dist/wails/.gitkeep
ARG VERSION=dev
ARG TARGETOS TARGETARCH TARGETVARIANT
RUN CGO_ENABLED=0 GOWORK=off GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath \
      -ldflags "-s -w -X github.com/otfabric/sunspec-explorer/internal/buildinfo.Version=${VERSION}" \
      -o /out/sunspec-explorerd ./cmd/sunspec-explorerd
# The data directory, as the daemon insists on it: its own, closed to others.
# A volume mounted over it starts with these permissions.
RUN mkdir /out/data

# No shell, no libc, no package manager: the static binary and a user.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
LABEL org.opencontainers.image.title="sunspec-explorerd" \
      org.opencontainers.image.description="OT Fabric SunSpec Explorer daemon: read-only SunSpec engine with a web UI" \
      org.opencontainers.image.source="https://github.com/otfabric/sunspec-explorer" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/sunspec-explorerd /usr/local/bin/sunspec-explorerd
COPY --from=build --chown=nonroot:nonroot --chmod=0700 /out/data /data
ENV SUNSPEC_EXPLORER_DATA_DIR=/data
VOLUME /data
EXPOSE 8765
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/sunspec-explorerd"]
# A published port reaches the container from outside its loopback, so the
# daemon must listen on every address; the token and the Host check apply.
CMD ["--listen", "0.0.0.0:8765", "--allow-remote-bind"]
