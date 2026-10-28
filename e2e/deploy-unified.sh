#!/usr/bin/env bash
# Deploy ONE artifact Deployment+Service mounting every protocol. This is the
# "unified mount" topology: the client is pointed straight at the single
# Service and reaches every protocol by path (/artifacts/<proto>/...). No
# sidecar and no per-protocol self-base map (explicit addressing only).
set -euo pipefail
NS="${NS:-temp}"
IMAGE="${IMAGE:-forgejo.develop.10.199.64.20.nip.io/easy-platform/artifact:latest}"
UPSTREAM_PROXY="${UPSTREAM_PROXY:-http://mihomo.develop.svc.cluster.local:7890}"
NAME="${NAME:-artifact-unified}"
# Protocol list mirrors run.sh's default matrix.
PROTOCOLS="${PROTOCOLS:-npm pypi go cargo maven nuget rubygems composer hex pub helm conan swift conda nix huggingface protobuf debian apk rpm oci git ivy hackage cran cpan luarocks juliapkg google gradle clojars spring jitpack jsrnpm opam stackage bazel jenkins gitlfs netcache system}"

# proto_mount maps an e2e row onto the artifact adapter that serves it
# (mirrors ride an existing adapter), so --protocols lists distinct adapters.
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    gitlfs) echo git ;;
    *) echo "$1" ;;
  esac
}

list=""
seen_mount=""
for p in $PROTOCOLS; do
  mount="$(proto_mount "$p")"
  case ",${seen_mount}," in
    *",${mount},"*) : ;;
    *) seen_mount="${seen_mount:+${seen_mount},}${mount}"
       list="${list:+${list},}${mount}" ;;
  esac
done

self_base="http://${NAME}.${NS}.svc.cluster.local"
cat <<YAML | kubectl apply -n "${NS}" -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${NAME}
  labels: { app: artifact-unified }
spec:
  replicas: 1
  strategy: { type: Recreate }
  selector: { matchLabels: { app: ${NAME} } }
  template:
    metadata: { labels: { app: ${NAME} } }
    spec:
      containers:
      - name: artifact
        image: ${IMAGE}
        args: ["--listen=:8080", "--data=/data", "--protocols=${list}", "--self-base=${self_base}"]
        ports: [{ name: http, containerPort: 8080 }]
        resources:
          requests: { cpu: 200m, memory: 256Mi }
          limits:   { cpu: "2", memory: 2Gi }
        env:
        - name: HTTP_PROXY
          value: "${UPSTREAM_PROXY}"
        - name: HTTPS_PROXY
          value: "${UPSTREAM_PROXY}"
        - name: NO_PROXY
          value: "localhost,127.0.0.1,.svc.cluster.local,.svc,.nip.io"
        - name: EASYBLOBS_MAX_BODY
          value: "34359738368"
        readinessProbe:
          httpGet: { path: /readyz, port: http }
          initialDelaySeconds: 3
          periodSeconds: 5
        livenessProbe:
          httpGet: { path: /healthz, port: http }
          initialDelaySeconds: 15
          periodSeconds: 20
        volumeMounts: [{ name: data, mountPath: /data }]
      volumes: [{ name: data, emptyDir: {} }]
---
apiVersion: v1
kind: Service
metadata:
  name: ${NAME}
  labels: { app: artifact-unified }
spec:
  selector: { app: ${NAME} }
  ports: [{ name: http, port: 80, targetPort: http }]
YAML
echo "deployed unified: ${NAME} (${list})"
