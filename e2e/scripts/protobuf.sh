#!/bin/sh
# protobuf / BSR: publish a module or plugin INTO the mirror, then read it back.
#
# This used to be a read-only passthrough, so the suite only proved that a
# module could be DOWNLOADED through the mirror. Publishing your own buf module
# (or a self-written plugin) was impossible. The adapter now implements the
# BSR's own PushService, so this suite drives the real publish/read cycle.
#
# The wire format is the Connect protocol's JSON encoding of the BSR protos
# (buf/alpha/registry/v1alpha1/{push,download}.proto):
#
#   POST /buf.alpha.registry.v1alpha1.PushService/PushManifestAndBlobs
#        { owner, repository, manifest{content}, blobs[{digest{...},content}], tags }
#     -> { localModulePin{ owner, repository, commit, manifestDigest } }
#
#   POST /buf.alpha.registry.v1alpha1.DownloadService/Download
#        { owner, repository, reference }
#     -> { module{ files[{path, content}] } }
#
# `content`, and the digests, are base64 in that JSON encoding; the digest is
# SHAKE256 (64 bytes, lowercase hex once decoded).
#
# With BUF_BIN set, the suite ALSO drives the real `buf` CLI against the same
# server, because a hand-built request can be accidentally self-consistent
# while the actual client disagrees.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"

BASE="${EASYBLOBS_BASE%/}"
# A per-run repository name so a rerun against the same data dir cannot pass on
# leftovers from a previous run.
STAMP="$(date +%s)-$$"
OWNER="acme"
REPO="plugin-$STAMP"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Module content: a tiny proto module plus its buf.yaml. This is exactly what a
# self-written buf plugin package looks like.
#
# The module's NAME carries the registry host: `buf push` resolves it from here
# and calls <host>/buf.alpha.registry.v1alpha1.PushService/... . Naming the
# mirror as the host is what makes the CLI talk to THIS server.
# The registry HOST carries no port: buf dials https://<host>:443, and the TLS
# terminator in this suite forwards 443 -> the mirror's own port. Putting :8080
# here would make buf dial https://<host>:8080 instead and miss the terminator.
BUF_REGISTRY_HOST="${BUF_REGISTRY_HOST:-bsr.easyblobs.test}"
mkdir -p "$TMP/mod/proto/acme/plugin/v1"
# buf.yaml v2 rule (straight from the CLI):
#   "top-level name key cannot be specified if modules are specified, you must
#    specify the name on each individual module"
# so the registry host goes on the MODULE, not on the document.
cat > "$TMP/mod/buf.yaml" <<YAML
version: v2
modules:
  - path: proto
    name: $BUF_REGISTRY_HOST/acme/$REPO
YAML
cat > "$TMP/mod/proto/acme/plugin/v1/plugin.proto" <<'PROTO'
syntax = "proto3";

package acme.plugin.v1;

// A request the plugin answers.
message Request { string name = 1; }

// The plugin's reply.
message Reply { string greeting = 1; }

service Greeter {
  rpc Greet(Request) returns (Reply);
}
PROTO

# Build the push request with python3 (base64 + SHAKE256 are not shell jobs).
# SHAKE256 comes from hashlib, which is in the standard library.
python3 - "$TMP/mod" "$OWNER" "$REPO" "$TMP/push.json" <<'PY'
import base64, hashlib, json, os, sys

moddir, owner, repo, out = sys.argv[1:5]

paths = []
for root, _, names in os.walk(moddir):
    for n in sorted(names):
        full = os.path.join(root, n)
        paths.append(os.path.relpath(full, moddir).replace(os.sep, "/"))
paths.sort()

def shake(b: bytes) -> bytes:
    return hashlib.shake_256(b).digest(64)

blobs, manifest = [], []
for p in paths:
    content = open(os.path.join(moddir, p), "rb").read()
    dig = shake(content)
    manifest.append(f"shake256:{dig.hex()}  {p}\n")
    blobs.append({
        "digest": {
            "digestType": 1,
            "digest": base64.b64encode(dig).decode(),
        },
        "content": base64.b64encode(content).decode(),
    })

manifest_bytes = "".join(manifest).encode()
req = {
    "owner": owner,
    "repository": repo,
    "manifest": {
        "digest": {
            "digestType": 1,
            "digest": base64.b64encode(shake(manifest_bytes)).decode(),
        },
        "content": base64.b64encode(manifest_bytes).decode(),
    },
    "blobs": blobs,
    "tags": ["v1.0.0"],
}
open(out, "w").write(json.dumps(req))
print("built push request for", len(paths), "file(s)")
PY

# AUTH: the push must carry the write credential when one is configured.
AUTH_ARGS=""
if [ -n "${EASYBLOBS_TOKEN:-}" ]; then
  AUTH_ARGS="-H Authorization: Bearer ${EASYBLOBS_TOKEN}"
fi

# A push without a credential must be refused when auth is on.
if [ "${EASYBLOBS_REQUIRE_AUTH:-0}" = "1" ]; then
  code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST \
    "$BASE/buf.alpha.registry.v1alpha1.PushService/PushManifestAndBlobs" \
    -H 'Content-Type: application/json' --data-binary "@$TMP/push.json")
  case "$code" in
    401|403) echo "protobuf: unauthenticated push refused ($code)" ;;
    *) echo "protobuf: FAIL unauthenticated push returned $code, want 401/403" >&2; exit 1 ;;
  esac
fi

echo "protobuf: pushing $OWNER/$REPO"
code=$(curl -sS -o "$TMP/pushresp.json" -w '%{http_code}' -X POST \
  "$BASE/buf.alpha.registry.v1alpha1.PushService/PushManifestAndBlobs" \
  -H 'Content-Type: application/json' $AUTH_ARGS --data-binary "@$TMP/push.json")
[ "$code" = "200" ] || {
  echo "protobuf: FAIL push -> HTTP $code" >&2
  head -c 400 "$TMP/pushresp.json" >&2; echo >&2; exit 1; }

COMMIT=$(python3 - "$TMP/pushresp.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
pin = d.get("localModulePin") or {}
print(pin.get("commit", ""))
PY
)
[ -n "$COMMIT" ] || { echo "protobuf: FAIL push returned no commit (localModulePin)" >&2; exit 1; }
echo "protobuf: pushed commit $COMMIT"

# Read the module back and compare every file BYTE FOR BYTE. A push that stores
# something merely plausible is indistinguishable from a broken one until the
# bytes are compared.
cat > "$TMP/dl.json" <<JSON
{"owner":"$OWNER","repository":"$REPO","reference":"$COMMIT"}
JSON
code=$(curl -sS -o "$TMP/dlresp.json" -w '%{http_code}' -X POST \
  "$BASE/buf.alpha.registry.v1alpha1.DownloadService/Download" \
  -H 'Content-Type: application/json' --data-binary "@$TMP/dl.json")
[ "$code" = "200" ] || {
  echo "protobuf: FAIL download -> HTTP $code" >&2
  head -c 400 "$TMP/dlresp.json" >&2; echo >&2; exit 1; }

python3 - "$TMP/dlresp.json" "$TMP/mod" <<'PY'
import base64, json, os, sys

resp = json.load(open(sys.argv[1]))
moddir = sys.argv[2]
files = (resp.get("module") or {}).get("files") or []
if not files:
    raise SystemExit("download returned no files")

want = {}
for root, _, names in os.walk(moddir):
    for n in names:
        full = os.path.join(root, n)
        want[os.path.relpath(full, moddir).replace(os.sep, "/")] = open(full, "rb").read()

got = {f["path"]: base64.b64decode(f["content"]) for f in files}
if set(got) != set(want):
    raise SystemExit(f"file set differs: got {sorted(got)} want {sorted(want)}")
for p, b in want.items():
    if got[p] != b:
        raise SystemExit(f"file {p} came back different")
print(f"protobuf: download returned all {len(want)} file(s) byte-for-byte")
PY

# The manifest+blobs form must describe the same module.
code=$(curl -sS -o "$TMP/mbresp.json" -w '%{http_code}' -X POST \
  "$BASE/buf.alpha.registry.v1alpha1.DownloadService/DownloadManifestAndBlobs" \
  -H 'Content-Type: application/json' --data-binary "@$TMP/dl.json")
[ "$code" = "200" ] || {
  echo "protobuf: FAIL manifest download -> HTTP $code" >&2
  head -c 400 "$TMP/mbresp.json" >&2; echo >&2; exit 1; }

python3 - "$TMP/mbresp.json" <<'PY'
import base64, json, sys

resp = json.load(open(sys.argv[1]))
man = (resp.get("manifest") or {}).get("content")
if not man:
    raise SystemExit("manifest download returned no manifest content")
text = base64.b64decode(man).decode()
lines = [l for l in text.splitlines() if l]
if not lines:
    raise SystemExit("the manifest is empty")
for l in lines:
    if not l.startswith("shake256:") or "  " not in l:
        raise SystemExit(f"malformed manifest line: {l!r}")
if not resp.get("blobs"):
    raise SystemExit("manifest download returned no blobs")
print(f"protobuf: manifest lists {len(lines)} file(s) and {len(resp['blobs'])} blob(s)")
PY

echo "protobuf: OK (pushed a module/plugin and read it back byte-for-byte)"

# ---------------------------------------------------------------------------
# Optional: drive the REAL buf CLI against the same server. A hand-built
# request can be accidentally self-consistent while the actual client
# disagrees, so this runs when the binary is available.
# ---------------------------------------------------------------------------
if [ -n "${BUF_BIN:-}" ] && [ -x "${BUF_BIN}" ]; then
  echo "protobuf: driving the real buf CLI"

  # buf reaches a registry as HTTPS on <host>:443 - the scheme is implied by the
  # name in buf.yaml and a URL cannot be given there. The mirror serves plain
  # HTTP on :8080, so stand up a TLS terminator on 443 in front of it with the
  # certificate the job generated. That is exactly how an on-prem BSR sits
  # behind a TLS proxy, so it is a faithful test of the publish path.
  if [ -n "${BUF_TLS_CERT:-}" ] && [ -n "${BUF_TLS_KEY:-}" ] &&
     [ -s "$BUF_TLS_CERT" ] && [ -s "$BUF_TLS_KEY" ]; then
    if command -v socat >/dev/null; then
      PORT="${EASYBLOBS_PORT:-8080}"
      # :443 is a PRIVILEGED port: binding it as an unprivileged user fails with
      #   socat[..] E bind(6, {AF=10 [...]:443}, 28): Permission denied
      # so the terminator runs under sudo when we are not already root.
      #
      # -v logs the forwarded bytes, which is how a "buf says 404 but the server
      # saw nothing" case gets distinguished from "buf never reached us".
      SOCAT_CMD="socat -v OPENSSL-LISTEN:443,fork,reuseaddr,cert=$BUF_TLS_CERT,key=$BUF_TLS_KEY,verify=0 TCP:127.0.0.1:$PORT"
      if [ "$(id -u)" -eq 0 ]; then
        sh -c "$SOCAT_CMD" >/tmp/socat.log 2>&1 &
      else
        sudo sh -c "$SOCAT_CMD" >/tmp/socat.log 2>&1 &
      fi
      SOCAT_PID=$!
      sleep 2
      # Probe the PORT, not the PID: under sudo the process we spawned is the
      # sudo wrapper, so 'kill -0' does not tell us whether socat is listening.
      up=0
      i=0
      while [ "$i" -lt 20 ]; do
        if command -v openssl >/dev/null &&
           echo | openssl s_client -connect "127.0.0.1:443" -servername "$BUF_REGISTRY_HOST" \
             -CAfile "$BUF_TLS_CERT" >/dev/null 2>&1; then
          up=1; break
        fi
        i=$((i+1)); sleep 0.5
      done
      if [ "$up" -eq 1 ]; then
        echo "protobuf: TLS terminator is serving on :443 -> 127.0.0.1:$PORT"
        # Prove the WHOLE path from the loopback name buf will use: DNS name ->
        # 443 -> terminator -> mirror -> handler. If this fails, the push failure
        # is a plumbing problem and not a protocol one.
        glue_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 10 \
          --resolve "$BUF_REGISTRY_HOST:443:127.0.0.1" \
          --cacert "$BUF_TLS_CERT" \
          -X POST -H 'Content-Type: application/json' \
          -d '{"moduleRefs":[{"value":{"name":{"owner":"probe","module":"probe"}}}]}' \
          "https://$BUF_REGISTRY_HOST/buf.registry.module.v1.ModuleService/GetModules" 2>/dev/null || echo 000)
        echo "protobuf: end-to-end probe through the TLS path -> HTTP $glue_code"
      else
        echo "protobuf: WARNING the TLS terminator did not come up; see /tmp/socat.log" >&2
        cat /tmp/socat.log >&2 || true
      fi
    else
      echo "protobuf: WARNING socat is not available; buf cannot reach an HTTP mirror" >&2
    fi
  else
    echo "protobuf: WARNING no TLS material provided; buf needs HTTPS to the registry" >&2
  fi

  cd "$TMP/mod"
  "$BUF_BIN" --version
  if [ -n "${EASYBLOBS_TOKEN:-}" ]; then
    export BUF_TOKEN="${EASYBLOBS_TOKEN}"
  fi
  # HOW buf chooses a registry:
  #
  #   buf push <source> [flags]
  #
  # The argument is a SOURCE (a directory), NOT a URL:
  #   Failure: invalid dir path: "http://127.0.0.1:8080/artifacts/protobuf"
  #
  # The registry comes from the module NAME in buf.yaml:
  #   name: buf.build/acme/plugin      -> push to buf.build
  #   name: <host>/acme/plugin         -> push to <host>
  #
  # So pointing the CLI at this mirror means naming it in buf.yaml. The host is
  # the registry, and this server must be reachable under that name - the
  # harness sets up a hosts entry for exactly that.
  #
  # The source is "." and the output goes to a FILE so buf's OWN exit status can
  # be checked. 'if buf push | tail' tests tail's status, not buf's, which is how
  # a failing push once printed "succeeded".
  # Run buf with its output straight to a file, then PRINT the file. Redirecting
  # to a file first is what makes the exit status buf's own:
  #
  #   buf push . | tee log        -> '$?' is TEE's status, not buf's
  #   set -o pipefail             -> NOT supported by dash, which is what
  #                                  ci-run.sh uses; the script died silently on
  #                                  it before buf ever ran
  set +e
  "$BUF_BIN" push . >"$TMP/bufpush.log" 2>&1
  buf_rc=$?
  set -e
  echo "protobuf: buf push rc=$buf_rc"
  echo "protobuf: buf push output was:"
  cat "$TMP/bufpush.log"
  echo "protobuf: (end of buf output)"
  echo "protobuf: module config was:"
  cat buf.yaml
  echo "protobuf: ---"
  if [ "$buf_rc" -eq 0 ]; then
    echo "protobuf: buf push succeeded (exit 0) - the REAL client published here"
  else
    # A buf CLI failure is a REAL finding, not something to shrug at: the whole
    # point of this branch is to check what the actual client does with the
    # mirror. Report the whole log so the reason is visible.
    echo "protobuf: FAIL buf push exited $buf_rc" >&2
    cat "$TMP/bufpush.log" >&2
    exit 1
  fi
fi
