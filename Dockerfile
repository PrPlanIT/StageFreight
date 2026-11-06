# ---- Execution substrate sources ----
# Docker CLI + buildx come from their official images, pinned like every other image and
# resolved for the build's platform.
FROM docker.io/library/docker:29.9.3-cli AS docker-cli
FROM docker.io/docker/buildx-bin:0.38.2 AS buildx

# ---- Go build stage ----
FROM docker.io/library/golang:1.27.3-alpine3.24 AS builder

RUN apk add --no-cache git chafa

WORKDIR /src

# Module download — cached independently of source changes.
# Only re-runs when go.mod/go.sum change.
COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Source copy — invalidates build layers but NOT module cache.
COPY toolchains.go ./
COPY .stagefreight/toolchains.lock ./.stagefreight/
COPY src/ ./src/
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Tidy with mount cache — uses cached modules, only adds missing ones.
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod tidy

# Generate banner art from logo.png (produces banner_art_gen.go with escaped ANSI).
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go generate ./src/output/...

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# Build with persistent Go build cache — only recompiles changed packages.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=0 go build -tags banner_art \
      -ldflags "-s -w \
        -X github.com/PrPlanIT/StageFreight/src/version.Version=${VERSION} \
        -X github.com/PrPlanIT/StageFreight/src/version.Commit=${COMMIT} \
        -X github.com/PrPlanIT/StageFreight/src/version.BuildDate=${BUILD_DATE}" \
      -o /out/stagefreight ./src/cli

# ---- Runtime image ----
FROM docker.io/library/alpine:3.24.3

LABEL maintainer="PrPlanIT <precisionplanit@gmail.com>" \
      org.opencontainers.image.title="StageFreight" \
      org.opencontainers.image.description="A declarative lifecycle runtime that governs Git as the source of truth, enforcing operator-defined intent across GitOps workflows, Kubernetes, Docker, and CI ecosystems." \
      org.opencontainers.image.source="https://github.com/PrPlanIT/StageFreight" \
      org.opencontainers.image.url="https://hub.docker.com/r/prplanit/stagefreight" \
      org.opencontainers.image.documentation="https://github.com/PrPlanIT/StageFreight#readme" \
      org.opencontainers.image.licenses="AGPL-3.0-only" \
      org.opencontainers.image.vendor="PrPlanIT"

# No runtime packages: git is intentionally absent, as the go-git pure-Go transport
# handles all repository operations natively (SSH via golang.org/x/crypto/ssh, HTTPS via
# net/http). The banner art is rendered at build time, so chafa stays in the builder.

# UTF-8 locale for the banner's Unicode block characters in CI logs.
ENV LANG=C.UTF-8

# Execution substrate — Docker CLI + buildx stay in the image.
# All other tools (trivy, syft, grype, osv-scanner, cosign, flux, kubectl, go)
# are resolved at runtime by the StageFreight toolchain subsystem:
# downloaded, checksum-verified, cached, and executed by absolute path.
COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker

# buildx goes in the system plugin path /usr/local/lib/docker/cli-plugins, NOT ~/.docker:
# the Docker CLI searches the system plugin dirs regardless of $HOME, so buildx resolves
# under GitHub container jobs (HOME=/github/home) as well as root. A ~/.docker install is
# invisible there.
COPY --from=buildx /buildx /usr/local/lib/docker/cli-plugins/docker-buildx

# /stagefreight/cache — persistent (mount a volume here for cross-run reuse).
# Unprivileged by default, and any uid works: the writable paths belong to group 0 and
# are group-writable, and StageFreight falls back to a private HOME when HOME isn't
# writable. Build tools are provisioned at runtime without root — nothing here needs it.
RUN adduser -D -u 1000 -G root -h /home/stagefreight -s /bin/sh stagefreight && \
    mkdir -p /stagefreight/cache && \
    chgrp -R 0 /stagefreight /home/stagefreight && \
    chmod -R g=u /stagefreight /home/stagefreight
ENV HOME=/home/stagefreight

# Copy the Go binary from builder stage.
COPY --from=builder /out/stagefreight /usr/local/bin/stagefreight

USER 1000
CMD ["/bin/sh"]
