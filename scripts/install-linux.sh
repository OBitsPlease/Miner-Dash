#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo "run as root" >&2; exit 1; }
[[ -r /etc/os-release ]] || { echo "cannot identify operating system" >&2; exit 1; }
# shellcheck disable=SC1091
source /etc/os-release
[[ ${ID:-} == ubuntu && ${VERSION_ID:-} == 24.04 ]] ||
  { echo "Ubuntu 24.04 LTS is required" >&2; exit 1; }

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
linux="$root/deploy/linux"
[[ -d "$linux" ]] || { echo "deploy/linux not found beside scripts" >&2; exit 1; }

mapfile -t packages < <(sed -E '/^[[:space:]]*(#|$)/d; s/[[:space:]]+$//' "$linux/packages.txt")
((${#packages[@]} > 0)) || { echo "empty package manifest" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends "${packages[@]}"

install -d -o root -g root -m 0700 /etc/minerdash /etc/minerdash/miners
install -d -o root -g root -m 0700 /var/lib/minerdash
install -d -o root -g root -m 0755 /opt/minerdash/agent/releases
install -d -o root -g root -m 0700 /opt/minerdash/miners
install -d -o root -g root -m 0755 /usr/share/doc/minerdash-linux
install -o root -g root -m 0644 "$linux/OPERATIONS.md" /usr/share/doc/minerdash-linux/OPERATIONS.md
install -o root -g root -m 0644 "$linux/minerdash-agent.service" /etc/systemd/system/minerdash-agent.service
install -d -o root -g root -m 0755 /etc/systemd/journald@minerdash.conf.d
install -o root -g root -m 0644 "$linux/journald-minerdash.conf" \
  /etc/systemd/journald@minerdash.conf.d/60-retention.conf
systemctl daemon-reload
systemctl try-restart systemd-journald@minerdash.service || true
echo "Base provisioning complete. Stage an agent release, then enroll this rig."
