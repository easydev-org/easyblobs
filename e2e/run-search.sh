#!/usr/bin/env bash
# Search/aux matrix: for each protocol, drive the client's SEARCH (or other
# auxiliary) endpoints against the artifact Service's explicit URL and assert
# useful results. This complements run.sh (which only exercises
# install/download): the goal is that an uncached public query returns real
# upstream results, merged with any locally-published packages.
#
# There is no intercepting sidecar: each search command reads EASYBLOBS_BASE and
# points its client at it.
#
# Usage:
#   bash run-search.sh
#   PROTOCOLS="npm cargo" bash run-search.sh
#   KEEP=1 PROTOCOLS=go bash run-search.sh
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
NS="${NS:-temp}"
TOOL_IMAGE_PREFIX="${TOOL_IMAGE_PREFIX:-forgejo.develop.10.199.64.20.nip.io/easy-platform/tool}"
TOOL_TAG="${TOOL_TAG:-latest}"
PROTOCOLS=(${PROTOCOLS:-npm cargo rubygems composer hex go pypi nuget helm conda rpm})
KEEP="${KEEP:-0}"
# JOBS protocols run concurrently (each has its own pod). JOBS=1 is serial.
JOBS="${JOBS:-6}"

source "${HERE}/parallel.sh"

# proto_mount maps a row to the artifact adapter that serves it.
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    gitlfs) echo git ;;
    *) echo "$1" ;;
  esac
}

proto_svc() {
  local p="$1"
  if [ -n "${UNIFIED_SVC:-}" ]; then
    echo "${UNIFIED_SVC}.${NS}.svc.cluster.local"
  else
    echo "artifact-${p}.${NS}.svc.cluster.local"
  fi
}

proto_img() {
  local p="$1"
  case "$p" in
    google|gradle|clojars|spring|jitpack) echo "${TOOL_IMAGE_PREFIX}-maven:${TOOL_TAG}" ;;
    jsrnpm)  echo "${TOOL_IMAGE_PREFIX}-npm:${TOOL_TAG}" ;;
    jsr)     echo "${TOOL_IMAGE_PREFIX}-haskell:${TOOL_TAG}" ;;
    gitlfs)  echo "${TOOL_IMAGE_PREFIX}-gitlfs:${TOOL_TAG}" ;;
    opam|stackage|bazel|jenkins) echo "${TOOL_IMAGE_PREFIX}-haskell:${TOOL_TAG}" ;;
    netcache) echo "${TOOL_IMAGE_PREFIX}-git:${TOOL_TAG}" ;;
    rpm|apk|nix) echo "${TOOL_IMAGE_PREFIX}-$p:latest" ;;
    *)       echo "${TOOL_IMAGE_PREFIX}-$p:${TOOL_TAG}" ;;
  esac
}

# search_cmd <proto>: the shell command run in the tool image. It must exit
# non-zero when the search yields no upstream results. It reads EASYBLOBS_BASE.
search_cmd() {
  case "$1" in
    npm)
      cat <<'SH'
cd /tmp && npm search express --registry="$EASYBLOBS_BASE/" --no-audit --no-fund --json 2>/dev/null \
  | node -e 'let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{const o=JSON.parse(d);if(!o.length)process.exit(1);console.log("npm search:",o.length,"hits, first",o[0].name)})'
SH
      ;;
    cargo)
      cat <<'SH'
cd /tmp && out="$(cargo search anyhow 2>/dev/null)" && echo "$out" | grep -q anyhow && echo "cargo search: $(echo "$out" | head -1)"
SH
      ;;
    rubygems)
      cat <<'SH'
api="$(curl -s "$EASYBLOBS_BASE/api/v1/search.json?query=thor")" \
  && echo "$api" | grep -q '"name":"thor"' \
  && echo "rubygems search: $(echo "$api" | head -c 60)"
names="$(curl -s "$EASYBLOBS_BASE/names")" && echo "$names" | grep -qx thor && echo "rubygems names: thor present"
SH
      ;;
    composer)
      cat <<'SH'
cd /tmp && composer config -g repos.packagist composer "$EASYBLOBS_BASE/" >/dev/null 2>&1
out="$(composer search monolog 2>/dev/null)" && echo "$out" | grep -qi monolog && echo "composer search: $(echo "$out" | head -1)"
SH
      ;;
    hex)
      cat <<'SH'
out="$(curl -s "$EASYBLOBS_BASE/api/packages?search=jason")" \
  && echo "$out" | grep -qi '"name":"jason"' \
  && echo "hex search: ok ($(echo "$out" | head -c 40))"
SH
      ;;
    go)
      cat <<'SH'
export HOME=/tmp/sg GOPATH=/tmp/sg/go GOMODCACHE=/tmp/sg/go/pkg/mod GOFLAGS=-mod=mod GOSUMDB=off
rm -rf "$HOME" && mkdir -p "$HOME/w" && cd "$HOME/w"
printf 'module t\n\ngo 1.21\n' > go.mod
printf 'package main\nimport _ "golang.org/x/text/language"\nfunc main(){}\n' > main.go
out="$(GOPROXY="$EASYBLOBS_BASE" timeout 180 go mod tidy 2>&1)" && echo "go tidy: ok ($(echo "$out" | tail -1))"
SH
      ;;
    pypi)
      cat <<'SH'
out="$(pip index versions six --index-url "$EASYBLOBS_BASE/simple/" 2>&1)" && echo "$out" | grep -qi six && echo "pip index: $(echo "$out" | head -1)"
SH
      ;;
    nuget)
      cat <<'SH'
cd /tmp && mkdir -p /root/.nuget/NuGet && cat > /root/.nuget/NuGet/NuGet.Config <<X
<?xml version="1.0" encoding="utf-8"?>
<configuration><packageSources><clear />
<add key="easy-platform" value="$EASYBLOBS_BASE/v3/index.json" allowInsecureConnections="true" />
</packageSources></configuration>
X
out="$(dotnet package search serilog --source "$EASYBLOBS_BASE/v3/index.json" --take 2 2>&1)" \
  && echo "$out" | grep -qi serilog && echo "nuget search: ok"
SH
      ;;
    helm)
      cat <<'SH'
helm repo add easy-platform "$EASYBLOBS_BASE" >/dev/null 2>&1 || true
helm repo update >/dev/null 2>&1
out="$(helm search repo easy-platform/redis 2>&1)" && echo "$out" | grep -q redis && echo "helm search: ok"
SH
      ;;
    conda)
      cat <<'SH'
conda tos accept --override-channels --channel "$EASYBLOBS_BASE/pkgs/main" >/dev/null 2>&1 || true
out="$(conda search --override-channels -c "$EASYBLOBS_BASE/pkgs/main" zlib 2>&1)" && echo "$out" | grep -q zlib && echo "conda search: ok"
SH
      ;;
    rpm)
      cat <<'SH'
rm -f /etc/yum.repos.d/*.repo
printf '[main]\ninstall_weak_deps=False\nreleasever=44\n' > /etc/dnf/dnf.conf
printf '[fedora]\nname=fedora\nbaseurl=%s/pub/fedora/linux/releases/43/Everything/x86_64/os/\nenabled=1\ngpgcheck=0\noptional_metadata_types=primary\n' "$EASYBLOBS_BASE" > /etc/yum.repos.d/fedora.repo
out="$(dnf -q --refresh search jq 2>&1)" && echo "$out" | grep -q jq && echo "dnf search: ok"
SH
      ;;
    *) echo "" ;;
  esac
}

write_script_cm() {
  local p="$1" name="search-$1"
  local tmp
  tmp="$(mktemp -d)"
  search_cmd "$p" >"${tmp}/${p}.sh"
  kubectl create configmap "${name}-script" -n "$NS" \
    --from-file="${p}.sh=${tmp}/${p}.sh" \
    --dry-run=client -o yaml | kubectl apply -n "$NS" -f - >/dev/null
  rm -rf "$tmp"
}

write_pod() {
  local p="$1" name="search-$1" img
  img="$(proto_img "$p")"
  cat <<YAML | kubectl apply -n "$NS" -f - >/dev/null
apiVersion: v1
kind: Pod
metadata: { name: ${name}, labels: { app: search } }
spec:
  containers:
  - name: tool
    image: ${img}
    imagePullPolicy: Always
    command: ["sleep", "1800"]
    resources:
      requests: { cpu: 100m, memory: 256Mi }
      limits:   { cpu: "2", memory: 4Gi }
    env:
    - { name: EASYBLOBS_BASE, value: "http://$(proto_svc "$p")/artifacts/$(proto_mount "$p")" }
    - { name: EASYBLOBS_URL, value: "http://$(proto_svc "$p")" }
    volumeMounts:
    - { name: script, mountPath: /scripts, readOnly: true }
  volumes:
  - { name: script, configMap: { name: ${name}-script } }
YAML
}

run_one() {
  local p="$1" name="search-$1" out rc
  if [ -z "$(search_cmd "$p")" ]; then
    echo "SKIP $p  (no search command)"
    emit_result "$p" "SKIP $p"
    return
  fi
  write_script_cm "$p"
  kubectl delete pod -n "$NS" "$name" --ignore-not-found --force --grace-period=0 >/dev/null 2>&1
  write_pod "$p"
  if ! kubectl wait -n "$NS" --for=condition=Ready "pod/${name}" --timeout=300s >/dev/null 2>&1; then
    echo "FAIL $p  (pod not ready)"
    emit_result "$p" "FAIL $p pod-not-ready"
    return
  fi
  out="$(kubectl exec -n "$NS" "$name" -c tool -- timeout 400 sh "/scripts/${p}.sh" 2>&1)"; rc=$?
  if [ "$rc" -eq 0 ]; then
    echo "PASS $p  $(echo "$out" | tail -1)"
    emit_result "$p" "PASS $p"
  else
    echo "FAIL $p  (rc=$rc)"
    emit_result "$p" "FAIL $p rc=$rc"
    echo "$out" | tail -10 | sed 's/^/      | /'
  fi
  [ "$KEEP" = "1" ] || kubectl delete pod -n "$NS" "$name" --ignore-not-found >/dev/null 2>&1
}

run_pool run_one "${PROTOCOLS[@]}"
