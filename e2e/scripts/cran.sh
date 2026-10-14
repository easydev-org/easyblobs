#!/bin/sh
# CRAN (R): a plain HTTP tree. Point install.packages at the artifact mount.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
d=/tmp/cran-lib
rm -rf "$d" && mkdir -p "$d"
Rscript -e "install.packages('jsonlite', lib='$d', repos='$EASYBLOBS_BASE', quiet=TRUE, dependencies=FALSE)" >/dev/null 2>&1
[ -d "$d/jsonlite" ] || { echo "cran: jsonlite not installed"; exit 1; }
Rscript -e ".libPaths('$d'); library(jsonlite); cat(sprintf('{\"ok\":%s}\n', toJSON(TRUE)))"
