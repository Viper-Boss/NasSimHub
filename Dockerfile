# syntax=docker/dockerfile:1.19@sha256:b6afd42430b15f2d2a4c5a02b919e98a525b785b1aaff16747d2f623364e39b6

ARG NODE_IMAGE=node:24.18.0-bookworm-slim@sha256:6f7b03f7c2c8e2e784dcf9295400527b9b1270fd37b7e9a7285cf83b6951452d
ARG GO_IMAGE=golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651
ARG RUNTIME_IMAGE=alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
ARG NGINX_IMAGE=nginx:1.30.4-alpine-slim@sha256:ddde39c6e51f02fde7410c2e9c234cf2d0a4c7bdbbe176aeb37d8ad7ab4eb58c
ARG DOCKER_CLI_IMAGE=docker:28.5.2-cli@sha256:625d9431a9f54c5a2bc90f24f0e1c3d55b1349fd857dd85035f98c2c9acbdd4d
# Embedded NKN relay node (nknd/nknc, static binaries). Pinned like every other
# base image: release images are built on GitHub, where an unpinned "latest"
# would silently put whatever nknorg pushed that day into the api image.
# `latest` is the only multi-architecture tag nknorg publishes (the numbered
# tags are amd64-only builds from 2021); this is its index as of 2026-01-09,
# covering amd64, arm64 and arm/v6.
#
# An offline NAS whose local copy is a different digest keeps its old
# behaviour with --build-arg NKN_IMAGE=nknorg/nkn:latest, the same way
# deploy/fnos/compose_build_api3.sh overrides GO_IMAGE.
ARG NKN_IMAGE=nknorg/nkn:latest@sha256:9a96013030545d71bdacee29922bb412a01bb71325ce246c36fb13623dfed07a

FROM ${NODE_IMAGE} AS web-builder

ARG VERSION=dev

ENV NODE_OPTIONS=--max-old-space-size=1536 \
    VITE_MODEMDECK_BUILD_ID=${VERSION}
WORKDIR /workspace/web

COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --include=dev --no-audit --no-fund

COPY web/ ./
RUN npm run build


FROM ${GO_IMAGE} AS go-toolchain

# Build machines that cannot reach deb.debian.org (several fnOS NAS boxes
# resolve it to a refused connection) can pass
# --build-arg APT_MIRROR=mirrors.aliyun.com without changing anything else;
# the default keeps the upstream sources. Same escape hatch as NKN_IMAGE.
ARG APT_MIRROR=deb.debian.org

ENV GOTOOLCHAIN=local \
    CGO_ENABLED=1 \
    GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
WORKDIR /workspace

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    sed -i "s|deb.debian.org|${APT_MIRROR}|g" /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y --no-install-recommends libopus-dev=1.3.1-3


FROM go-toolchain AS app-builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

COPY go.mod go.sum ./
# The NasSimHub Node protocol and client modules are vendored as local
# replacements; go.mod names them, so they must be present before download.
COPY third_party/nassimhub-node/ ./third_party/nassimhub-node/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY cmd/modemdeck/ ./cmd/modemdeck/
COPY cmd/modemdeck-updater/ ./cmd/modemdeck-updater/
COPY internal/ ./internal/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    app_version="${VERSION}" \
    && test -n "${app_version}" \
    && \
    GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -mod=readonly -tags=netgo,osusergo -trimpath -buildvcs=false \
    -ldflags="-s -w -linkmode=external -extldflags=-static \
      -X main.version=${app_version}" \
    -o /out/modemdeck ./cmd/modemdeck

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    updater_version="${VERSION}" \
    && test -n "${updater_version}" \
    && CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -mod=readonly -tags=netgo,osusergo -trimpath -buildvcs=false \
    -ldflags="-s -w -X main.version=${updater_version}" \
    -o /out/modemdeck-updater ./cmd/modemdeck-updater


FROM scratch AS modemdeck-artifact

COPY --from=app-builder /out/modemdeck /modemdeck


FROM ${DOCKER_CLI_IMAGE} AS updater-runtime

ARG VERSION=dev
ARG BUILD_DATE=unknown
ARG VCS_REF=unknown

COPY --from=app-builder /out/modemdeck-updater /usr/local/bin/modemdeck-updater
COPY LICENSE NOTICE.md THIRD_PARTY_NOTICES.md /usr/share/licenses/modemdeck/

LABEL org.opencontainers.image.title="ModemDeck Updater" \
      org.opencontainers.image.description="Digest-pinned update control plane for ModemDeck containers" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}"

USER 0:0
EXPOSE 8081
STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/modemdeck-updater"]


FROM ${NGINX_IMAGE} AS web-runtime

ARG VERSION=dev
ARG BUILD_DATE=unknown
ARG VCS_REF=unknown

COPY --chown=101:101 web/nginx.conf /etc/nginx/nginx.conf
COPY --chown=101:101 --chmod=0755 scripts/nginx-entrypoint.sh /usr/local/bin/modemdeck-web-entrypoint
COPY --from=web-builder --chown=101:101 /workspace/web/dist/ /usr/share/nginx/html/
COPY LICENSE NOTICE.md THIRD_PARTY_NOTICES.md /usr/share/licenses/modemdeck/

LABEL org.opencontainers.image.title="ModemDeck Web" \
      org.opencontainers.image.description="ModemDeck Web frontend and API reverse proxy" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}"

USER 101:101

EXPOSE 7575 7576 7577/tcp 7577/udp
STOPSIGNAL SIGQUIT

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -T 3 --no-check-certificate -O /dev/null https://127.0.0.1:7577/api/v1/health/live || exit 1

ENTRYPOINT ["/usr/local/bin/modemdeck-web-entrypoint"]
CMD ["-g", "daemon off;"]


FROM ${NKN_IMAGE} AS nkn-binaries

FROM ${RUNTIME_IMAGE} AS runtime

ARG VERSION=dev
ARG BUILD_DATE=unknown
ARG VCS_REF=unknown
ARG MODEMDECK_UID=10001
ARG MODEMDECK_GID=10001

# ca-certificates: 不钉版本——alpine 源会下架旧版本（20260611-r0 已 404，
# r97 构建时源上只剩 20260909-r0），钉死=定时炸弹；证书包本身无行为差异风险。
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g "${MODEMDECK_GID}" modemdeck \
    && adduser -S -D -H -h /nonexistent -s /sbin/nologin \
        -u "${MODEMDECK_UID}" -G modemdeck modemdeck \
    && mkdir -p /var/lib/modemdeck /run/modemdeck /tmp \
    && ln -s /var/lib/modemdeck /data \
    && chown -R "${MODEMDECK_UID}:${MODEMDECK_GID}" /var/lib/modemdeck /tmp \
    && chmod 0770 /var/lib/modemdeck \
    && chmod 0750 /run/modemdeck

COPY --from=app-builder /out/modemdeck /usr/local/bin/modemdeck
COPY --from=nkn-binaries /nkn/nknd /usr/local/bin/nknd
COPY --from=nkn-binaries /nkn/nknc /usr/local/bin/nknc
COPY --from=nkn-binaries /nkn/web /usr/local/lib/nkn/web
COPY --chmod=0755 scripts/docker-entrypoint.sh /usr/local/bin/modemdeck-entrypoint
COPY LICENSE NOTICE.md THIRD_PARTY_NOTICES.md /usr/share/licenses/modemdeck/

LABEL org.opencontainers.image.title="ModemDeck" \
      org.opencontainers.image.description="Self-hosted cellular communications console" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${VCS_REF}"

ENV MODEMDECK_LISTEN_ADDRESS=0.0.0.0:8080 \
    MODEMDECK_DATABASE_PATH=/var/lib/modemdeck/modemdeck.db \
    MODEMDECK_RECORDINGS_PATH=/data/recordings \
    MODEMDECK_AGENT_SOCKET=/run/modemdeck/agent.sock \
    MODEMDECK_NKN_NODE_DIR=/data/nkn-node \
    MODEMDECK_SECURE_COOKIES=true \
    MODEMDECK_HEALTHCHECK_URL=http://127.0.0.1:8080/api/v1/health

USER ${MODEMDECK_UID}:${MODEMDECK_GID}
WORKDIR /var/lib/modemdeck

EXPOSE 8080
STOPSIGNAL SIGTERM

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD wget -q -T 3 -O /dev/null "${MODEMDECK_HEALTHCHECK_URL}" || exit 1

ENTRYPOINT ["/usr/local/bin/modemdeck-entrypoint"]
