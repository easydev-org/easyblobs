# artifact: multi-protocol package registry (pull-through + hosted).
#
# Built WITH the cluster buildkitd: run ./build-image.sh (targets the shared
# buildkitd, then pushes to forgejo via skopeo). No local build daemon.
#
# The `# syntax` line below IS required: `RUN --mount=type=cache` is a
# dockerfile-frontend instruction that the built-in classic frontend does not
# understand. buildkitd resolves docker/dockerfile from docker.io, which its
# config mirrors through the in-cluster artifact registry, so no external
# network is needed (verified in-cluster).
#
# The cache mounts below matter even when buildkitd keeps a long-lived PVC:
# they keep the go module cache and the npm store OUT of the layer graph, so
# changing one source file does not invalidate a layer that holds hundreds of
# megabytes of dependencies.
# syntax=docker/dockerfile:1
#
# Build context: the artifact repo root (go.work + every protocol module +
# cmd/). All sibling modules resolve through the replace directives in
# cmd/go.mod (no network for them); third-party deps (modernc sqlite,
# ulikunitz/xz) resolve from GOPROXY through the build proxy.
ARG REGISTRY=forgejo.develop.10.199.64.20.nip.io/root
ARG ALPINE=3.24
# apk mirror for the base-image packages (git/curl/ca-certificates). The default
# dl-cdn.alpinelinux.org is unreachable/slow in-cluster; point it at artifact.
ARG APK_MIRROR=http://artifact.worker.svc.cluster.local/artifacts/apk/alpine

# ---- webui (node) ----
# Build the admin SPA from the easyblobs-webui repo (ui/dist is NOT committed).
FROM ${REGISTRY}/node:22-alpine AS webui
ARG HTTP_PROXY
ARG HTTPS_PROXY
ARG EASYBLOBS_WEBUI_REPO=https://github.com/easydev-org/easyblobs-webui.git
ARG EASYBLOBS_WEBUI_REF=main
ENV HTTP_PROXY=${HTTP_PROXY} \
    HTTPS_PROXY=${HTTPS_PROXY} \
    NO_PROXY=localhost,127.0.0.1,.svc.cluster.local,.svc,.nip.io,10.199.64.20,.develop.10.199.64.20.nip.io
# npm's cache is a cache MOUNT, not a layer: a re-cloned repo would otherwise
# re-download every dependency from the network on each build.
RUN --mount=type=cache,target=/root/.npm \
    apk add --no-cache git ca-certificates \
    && git clone --depth 1 --branch "${EASYBLOBS_WEBUI_REF}" "${EASYBLOBS_WEBUI_REPO}" /webui \
    && cd /webui && npm ci --no-audit --no-fund && OUT_DIR=dist npm run build

# ---- build ----
FROM ${REGISTRY}/golang:1.26-alpine AS build
ARG APK_MIRROR=http://artifact.worker.svc.cluster.local/artifacts/apk/alpine
# GOPROXY/GOSUMDB are overridable so an internal module (e.g.
# github.com/easy-platform/platform) can be resolved from the deployment's
# artifact Go proxy. Declared IN this stage (a pre-FROM ARG is not visible in
# the stage unless re-declared). Defaults keep the public-proxy behaviour.
ARG GOPROXY=https://proxy.golang.org
ARG GOSUMDB=sum.golang.org
ARG HTTP_PROXY=http://mihomo.develop.svc.cluster.local:7890
ARG HTTPS_PROXY=http://mihomo.develop.svc.cluster.local:7890
ENV HTTP_PROXY=${HTTP_PROXY} \
    HTTPS_PROXY=${HTTPS_PROXY} \
    NO_PROXY=localhost,127.0.0.1,.svc.cluster.local,.svc,.nip.io,10.199.64.20,.develop.10.199.64.20.nip.io \
    GOPROXY=${GOPROXY} \
    GOSUMDB=${GOSUMDB} \
    GOWORK=off \
    CGO_ENABLED=0
RUN printf '%s/v3.24/main\n%s/v3.24/community\n' "${APK_MIRROR}" "${APK_MIRROR}" > /etc/apk/repositories \
    && env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy apk add --no-cache ca-certificates git
WORKDIR /src
COPY . .
# Place the SPA at the go:embed path (system/ui/dist) before the Go build.
COPY --from=webui /webui/dist/ system/ui/dist/
# cmd/ is the server module: with GOWORK=off its replace directives resolve
# every sibling protocol module from the copied tree; third-party deps come
# from GOPROXY over the build proxy.
#
# The go module cache is a cache MOUNT so that editing one .go file does not
# re-download the whole dependency tree (with GOWORK=off every sibling module
# is resolved and compiled from the copied tree). The build cache
# (GOCACHE) is mounted too: it is what makes an unchanged package skip
# recompilation entirely instead of being recompiled from source every build.
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    cd cmd && go build -mod=mod -trimpath -ldflags="-s -w" -o /out/easyblobs .

# ---- runtime ----
FROM ${REGISTRY}/alpine:${ALPINE}
ARG APK_MIRROR=http://artifact.worker.svc.cluster.local/artifacts/apk/alpine
RUN printf '%s/v3.24/main\n%s/v3.24/community\n' "${APK_MIRROR}" "${APK_MIRROR}" > /etc/apk/repositories \
    && env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy apk add --no-cache ca-certificates curl
# No `git` binary: the git PROXY (bare local mirrors) was removed; the LFS
# adapter is pure Go (CAS-backed), so nothing shells out to git.
# /data holds the sqlite metadata + CAS blobs (mount a PVC/hostPath there).
COPY --from=build /out/easyblobs /usr/local/bin/easyblobs
ENV EASYVCS_HOME=/data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/easyblobs"]
CMD ["--listen=:8080", "--data=/data"]
