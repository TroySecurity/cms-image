# syntax=docker/dockerfile:1
#
# Derived cloudflared image: the pinned upstream image plus ONE static file,
# /probe (cmd/cfdprobe), the HEALTHCHECK program. Nothing else changes: same
# entrypoint, user and layers below. The upstream image is distroless (no shell,
# no curl), which is why the probe is a static Go binary.
#
# Both stages are pinned by digest. To move to another cloudflared version,
# change the digest of the last FROM (and CLOUDFLARED_VERSION in the workflow).

FROM --platform=$BUILDPLATFORM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS build
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
RUN GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -buildvcs=false -ldflags "-s -w" -o /out/probe ./cmd/cfdprobe

# cloudflared 2026.10.4 (index digest: linux/amd64 and linux/arm64).
FROM cloudflare/cloudflared@sha256:c6c7ae1d5dfdcb094ad344ed68ccffc26635c9c6be80965664c33baf064b0515
COPY --from=build /out/probe /probe
