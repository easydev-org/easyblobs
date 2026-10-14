#!/bin/sh
# Fedora dnf: point the repo baseurl at the artifact mount. The first path
# segment is the repo key; the rest mirrors the upstream path.
set -e
: "${EASYBLOBS_BASE:?EASYBLOBS_BASE not set}"
rm -f /etc/yum.repos.d/*.repo
printf '[main]\ninstall_weak_deps=False\nreleasever=44\n' > /etc/dnf/dnf.conf
printf '[fedora]\nname=fedora\nbaseurl=%s/pub/fedora/linux/releases/43/Everything/x86_64/os/\nenabled=1\ngpgcheck=0\noptional_metadata_types=primary\n' "$EASYBLOBS_BASE" > /etc/yum.repos.d/fedora.repo
dnf -y --refresh install jq
rpm -q jq
# Run assertion: execute the installed binary.
echo '{"ok":true}' | jq -e '.ok' >/dev/null
echo "dnf installed: $(jq --version)"
