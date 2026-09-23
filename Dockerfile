# sightpane — the backend and the dashboard in one image.
#   docker build -t sightpane .
#   docker run -p 8790:8790 -v sightpane-data:/data sightpane
#
# The dashboard lives in its own repository (github.com/sightpane/ui) but is
# served by this binary, because serving both from one origin removes the CORS
# and base-URL configuration a split deployment would otherwise need. So the
# build fetches it here, pinned to UI_REF.
#
#   docker build --build-arg UI_REF=v0.2.0 .   # pin the dashboard to a tag
#   docker build --build-arg UI_REF=none .     # API only, no dashboard
#
# When working on both at once, build the dashboard in its own checkout and
# point the running binary at it with SIGHTPANE_UI_DIR rather than rebuilding here.

# 1) The dashboard: a Flutter web build. SIGHTPANE_API_URL is left empty so the
#    built app talks to the same origin it was served from. Flutter comes from
#    the official tarball, pinned to the same version as the local SDK so a build
#    here matches a build on a workstation.
FROM debian:bookworm-slim AS dashboard
ARG FLUTTER_VERSION=3.47.0
ARG UI_REPO=https://github.com/sightpane/ui.git
ARG UI_REF=main
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git unzip xz-utils && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://storage.googleapis.com/flutter_infra_release/releases/stable/linux/flutter_linux_${FLUTTER_VERSION}-stable.tar.xz" | tar -xJ -C /opt
ENV PATH="/opt/flutter/bin:${PATH}"
RUN git config --global --add safe.directory /opt/flutter && flutter config --no-analytics --enable-web && flutter precache --web
WORKDIR /src
# UI_REF=none leaves the directory empty, and the runtime image then falls back
# to the placeholder page compiled into the binary.
RUN mkdir -p /out && if [ "$UI_REF" != "none" ]; then \
      git clone --depth 1 --branch "$UI_REF" "$UI_REPO" ui && \
      cd ui && flutter pub get && \
      flutter build web --release --dart-define=SIGHTPANE_API_URL= && \
      cp -a build/web/. /out/; \
    fi

# 1b) The browser SDK for script-tag users: github.com/sightpane/ts-sdk built
#     with tsup into one IIFE file, served by the binary at /js/sightpane.js.
#     SDK_REF=none skips it and that path answers 404.
FROM node:22-alpine AS sdk
ARG SDK_REPO=https://github.com/sightpane/ts-sdk.git
ARG SDK_REF=main
RUN apk add --no-cache git
WORKDIR /src
RUN mkdir -p /out && if [ "$SDK_REF" != "none" ]; then \
      git clone --depth 1 --branch "$SDK_REF" "$SDK_REPO" sdk && \
      cd sdk && npm ci --ignore-scripts && npm run build && \
      cp dist/sightpane.js /out/; \
    fi

# 2) The backend: Go with cgo off, which pgx allows because it is pure Go, and
#    which is what lets the binary run on the bare alpine below.
FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY . .
# `go build .` and not a separate `go mod download`: the latter fetches every
# module in go.mod, and internal/testdb pulls testcontainers and its docker and
# otel trees in for the tests alone. Building the main package downloads only
# what the binary actually imports. The cache mounts do the layer caching the
# copy-go.mod-first trick used to.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /sightpane .

# 3) The runtime image.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend /sightpane /usr/local/bin/sightpane
COPY --from=dashboard /out /app/ui
COPY --from=sdk /out /app/sdk
ENV SIGHTPANE_ADDR=:8790 \
    SIGHTPANE_DATA=/data \
    SIGHTPANE_UI_DIR=/app/ui \
    SIGHTPANE_SDK_JS=/app/sdk/sightpane.js \
    SIGHTPANE_DEFAULT_PROJECT=default \
    SIGHTPANE_DEFAULT_KEY=dev \
    SIGHTPANE_ADMIN_EMAIL=admin@sightpane.local \
    SIGHTPANE_ADMIN_PASSWORD=admin123
VOLUME /data
EXPOSE 8790
HEALTHCHECK --interval=30s --timeout=3s CMD wget -qO- http://127.0.0.1:8790/api/v1/health || exit 1
ENTRYPOINT ["sightpane"]
