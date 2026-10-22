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
# With `tls: { use: false }` buf talks plain HTTP to <host>, so the host must
# carry the port this server actually listens on.
BUF_REGISTRY_HOST="${BUF_REGISTRY_HOST:-bsr.easyblobs.test}"
BUF_REGISTRY_PORT="${BUF_REGISTRY_PORT:-${EASYBLOBS_PORT:-8080}}"
BUF_REGISTRY_ADDR="$BUF_REGISTRY_HOST:$BUF_REGISTRY_PORT"
mkdir -p "$TMP/mod/proto/acme/plugin/v1"
# buf.yaml v2 rule (straight from the CLI):
#   "top-level name key cannot be specified if modules are specified, you must
#    specify the name on each individual module"
# so the registry host goes on the MODULE, not on the document.
cat > "$TMP/mod/buf.yaml" <<YAML
version: v2
modules:
  - path: proto
    name: $BUF_REGISTRY_ADDR/acme/$REPO
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

  # buf does NOT require HTTPS - that was my assumption, and the code says
  # otherwise. In buf's own client config:
  #
  #   connectclient.WithAddressMapper(func(address string) string {
  #       if config.TLS == nil { return buftransport.PrependHTTP(address) }
  #       return buftransport.PrependHTTPS(address)
  #   })
  #
  # and certclient.NewClientTLSConfig returns (nil, nil) for `tls.use: false`.
  # So plain HTTP is a first-class mode, and no TLS terminator is needed.
  #
  # The switch lives in buf's GLOBAL config (appext ReadConfig):
  #   <ConfigDir>/config.yaml, with
  #     version: v1
  #     tls: { use: false }
  # and <ConfigDir> is overridable with the CONFIG_DIR environment variable,
  # which is how this runs without touching the runner's real config.
  BUF_CONFIG_DIR="${BUF_CONFIG_DIR:-$TMP/bufcfg}"
  mkdir -p "$BUF_CONFIG_DIR"
  # 'use' is a STRING in buf (certclient.ExternalClientTLSConfig{Use string}),
  # and NewClientTLSConfig matches it with strings.ToLower:
  #     case "false": return nil, nil
  # so the value must be QUOTED. A bare `use: false` is a YAML boolean, the
  # field fails to decode, and buf silently keeps its default (HTTPS) - which is
  # exactly what happened: the config was written, buf still dialled https.
  cat > "$BUF_CONFIG_DIR/config.yaml" <<'CFG'
version: v1
tls:
  use: "false"
CFG
  # The variable name is PREFIXED with the app name (appext getDirPath):
  #   dirPath := Env(getAppNameEnvPrefix(appName) + envSuffix)
  # so for buf it is BUF_CONFIG_DIR, not CONFIG_DIR. Setting the unprefixed name
  # silently does nothing, which is exactly what happened: the config file was
  # written, buf never read it, and buf still dialled with its default scheme.
  export BUF_CONFIG_DIR
  echo "protobuf: buf global config ($BUF_CONFIG_DIR/config.yaml):"
  cat "$BUF_CONFIG_DIR/config.yaml"
  # PROVE the config is actually read before trusting it. ReadConfig uses a
  # STRICT YAML decode, so an unknown key must make buf fail loudly - if it does
  # not, the file is not being loaded at all and every TLS conclusion drawn from
  # it would be worthless.
  probe_dir="$TMP/bufcfg-probe"
  mkdir -p "$probe_dir"
  printf 'version: v1\nnot_a_real_key: 1\n' > "$probe_dir/config.yaml"
  if BUF_CONFIG_DIR="$probe_dir" "$BUF_BIN" --version >"$TMP/probe.log" 2>&1; then
    echo "protobuf: NOTE buf accepted a config with an unknown key; BUF_CONFIG_DIR may be ignored"
  else
    echo "protobuf: strict config decode is active (unknown key rejected):"
    tail -3 "$TMP/probe.log"
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
