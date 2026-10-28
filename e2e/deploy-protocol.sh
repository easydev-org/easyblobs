#!/usr/bin/env bash
# Deploy one artifact Deployment+Service per protocol into the e2e namespace.
# Each deployment mounts exactly one protocol; its Service is artifact-<p> and
# the client is pointed straight at http://artifact-<p>.<ns>.svc.cluster.local
# /artifacts/<mount>/... (explicit addressing — no sidecar, no self-base map).
# artifact reaches its upstreams through the cluster proxy (HTTP_PROXY below).
set -euo pipefail
NS="${NS:-temp}"
IMAGE="${IMAGE:-forgejo.develop.10.199.64.20.nip.io/easy-platform/artifact:v0.28.0}"
UPSTREAM_PROXY="${UPSTREAM_PROXY:-http://mihomo.develop.svc.cluster.local:7890}"
PROTOCOLS=(${PROTOCOLS:-npm pypi go cargo maven nuget rubygems composer hex pub helm conan swift conda nix huggingface protobuf debian apk rpm oci git ivy hackage cran cpan luarocks juliapkg system google gradle clojars spring jitpack jsrnpm opam stackage bazel jenkins gitlfs netcache})

# proto_mount maps an e2e row to the artifact protocol it mounts (the
# maven-layout mirrors reuse the maven adapter; jsrnpm reuses npm).
proto_mount() {
  case "$1" in
    google|gradle|clojars|spring|jitpack) echo maven ;;
    jsrnpm) echo npm ;;
    gitlfs) echo git ;;
    *) echo "$1" ;;
  esac
}

for p in "${PROTOCOLS[@]}"; do
  name="artifact-${p}"
  mount="$(proto_mount "$p")"
  extra=""
  # The Service host is the client's explicit base; self-base is that same
  # origin so auth realms / self-URLs point back at this Service.
  self_base="http://${name}.${NS}.svc.cluster.local"
  if [ "$p" = "oci" ]; then
    extra=$'        - name: EASYBLOBS_MAX_BODY\n          value: "34359738368"\n'
  fi
  cat <<YAML | kubectl apply -n "${NS}" -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${name}
  labels: { app: artifact-e2e, protocol: "${p}" }
spec:
  replicas: 1
  strategy: { type: Recreate }
  selector: { matchLabels: { app: ${name} } }
  template:
    metadata: { labels: { app: ${name} } }
    spec:
      containers:
      - name: artifact
        image: ${IMAGE}
        args: ["--listen=:8080", "--data=/data", "--protocols=${mount}", "--self-base=${self_base}"]
        ports: [{ name: http, containerPort: 8080 }]
        resources:
          requests: { cpu: 50m, memory: 64Mi }
          limits:   { cpu: 500m, memory: 512Mi }
        env:
        - name: HTTP_PROXY
          value: "${UPSTREAM_PROXY}"
        - name: HTTPS_PROXY
          value: "${UPSTREAM_PROXY}"
        - name: NO_PROXY
          value: "localhost,127.0.0.1,.svc.cluster.local,.svc,.nip.io"
${extra}        readinessProbe:
          httpGet: { path: /readyz, port: http }
          initialDelaySeconds: 2
          periodSeconds: 5
        livenessProbe:
          httpGet: { path: /healthz, port: http }
          initialDelaySeconds: 10
          periodSeconds: 15
        volumeMounts: [{ name: data, mountPath: /data }]
      volumes: [{ name: data, emptyDir: {} }]
---
apiVersion: v1
kind: Service
metadata:
  name: ${name}
  labels: { app: artifact-e2e, protocol: "${p}" }
spec:
  selector: { app: ${name} }
  ports: [{ name: http, port: 80, targetPort: http }]
YAML
done
echo "deployed: ${PROTOCOLS[*]}"
