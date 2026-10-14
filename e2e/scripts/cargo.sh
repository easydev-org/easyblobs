#!/bin/sh
# Cargo: configure a sparse registry that points at the artifact mount. The
# sparse index lives at <base>/index/ (config.json + crate entries).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
mkdir -p /w/src "$CARGO_HOME"
cd /w
cat > "$CARGO_HOME/config.toml" <<X
[source.crates-io]
replace-with = "artifact"

[source.artifact]
registry = "sparse+$EASYBLOBS_BASE/index/"
X
printf '[package]\nname="t"\nversion="0.1.0"\nedition="2021"\n\n[dependencies]\nanyhow="1"\n' > Cargo.toml
cat > src/main.rs <<'X'
fn main() {
    let e = anyhow::anyhow!("boom");
    println!("cargo + anyhow: {}", e);
}
X
# Compile/run assertion (fetches deps + links a binary).
cargo run --quiet
