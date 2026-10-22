#!/bin/sh
# Hex (Elixir/Erlang): point HEX_MIRROR at the artifact mount. The mix client
# appends /tarballs, /names, /versions etc. to the mirror URL.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
# NO trailing slash. Hex appends "/packages/..." itself, so "base/" produced
# "//packages/jason"; this server answers that with a 307 redirect and hex does
# not follow redirects on the registry endpoint - it just times out with
#   exited in: GenServer.call(Hex.Registry.Server, {:versions, "hexpm", "jason"})
export HEX_MIRROR="${EASYBLOBS_BASE%/}"
mix local.rebar --force >/dev/null 2>&1 || true
# hex itself must be installed before it can be used. The project's own tool
# image bakes the archive at /opt/hex-archive/hex.ez; a plain elixir image does
# not have it, so fall back to fetching hex from hex.pm.
if [ -f /opt/hex-archive/hex.ez ]; then
  mix archive.install /opt/hex-archive/hex.ez --force >/dev/null
elif [ -n "${HEX_ARCHIVE_URL:-}" ]; then
  # A pre-fetched archive served by the harness. `mix local.hex` needs
  # HEX_MIRROR/installs/hex-1.x.csv, which a metadata-only mirror does not
  # implement (and an isolated run cannot reach hex.pm to bootstrap from), so
  # the archive is supplied out of band.
  curl -fsSL -o /tmp/hex.ez "$HEX_ARCHIVE_URL"
  mix archive.install /tmp/hex.ez --force >/dev/null
else
  # Last resort: bootstrap from hex.pm directly with the mirror temporarily
  # unset (the tool install is not what this suite measures).
  (
    unset HEX_MIRROR
    mix local.hex --force >/dev/null
  )
fi
# Create a project whose dependency is fetched from the mirror, then compile+run.
rm -rf ${E2E_W:-/w} && mix new ${E2E_W:-/w} --app w >/dev/null
cd ${E2E_W:-/w}
cat > mix.exs <<'X'
defmodule W.MixProject do
  use Mix.Project
  def project, do: [app: :w, version: "0.1.0", elixir: "~> 1.16", deps: [{:jason, "1.4.4"}]]
  def application, do: [extra_applications: [:logger]]
end
X
mix deps.get >/dev/null
mix compile >/dev/null
mix run -e 'IO.puts("elixir + jason: " <> Jason.encode!(%{ok: true}))'
