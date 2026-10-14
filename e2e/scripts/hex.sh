#!/bin/sh
# Hex (Elixir/Erlang): point HEX_MIRROR at the artifact mount. The mix client
# appends /tarballs, /names, /versions etc. to the mirror URL.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
export HEX_MIRROR="$EASYBLOBS_BASE/"
mix local.rebar --force >/dev/null 2>&1 || true
mix archive.install /opt/hex-archive/hex.ez --force >/dev/null
# Create a project whose dependency is fetched from the mirror, then compile+run.
rm -rf /w && mix new /w --app w >/dev/null
cd /w
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
