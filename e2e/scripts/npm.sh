#!/bin/sh
# npm: point the registry at the artifact mount (explicit URL, no sidecar).
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
cd /tmp
npm install --no-audit --no-fund --registry="$EASYBLOBS_BASE/" left-pad
# Compile/run assertion: use the installed package for real.
node -e 'const p=require("left-pad");const s=p("7",3,"0");if(s!=="007"){console.error("bad:",s);process.exit(1)}console.log("left-pad ->",s)'
