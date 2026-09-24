#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

die() { echo "$*" >&2; exit 1; }
usage() {
  echo "usage: $0 --agent FILE --agent-sha256 HEX" >&2
  echo "Run from a copied MinerDash source bundle as root on an existing Linux rig." >&2
  exit 2
}

[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run as root"
agent= agent_sha=
while (($#)); do
  case "$1" in
    --agent) [[ $# -ge 2 ]] || usage; agent=$2; shift 2 ;;
    --agent-sha256) [[ $# -ge 2 ]] || usage; agent_sha=${2,,}; shift 2 ;;
    *) usage ;;
  esac
done
[[ -f "$agent" && ! -L "$agent" ]] || die "agent must be a regular file"
[[ "$agent_sha" =~ ^[0-9a-f]{64}$ ]] || die "invalid agent SHA-256"
[[ $(sha256sum "$agent" | awk '{print $1}') == "$agent_sha" ]] || die "agent SHA-256 mismatch"
[[ $(uname -m) == x86_64 ]] || die "this installer requires an x86_64 rig"
command -v systemctl >/dev/null || die "systemd is required"

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
service="$root/deploy/linux/minerdash-agent.service"
journal="$root/deploy/linux/journald-minerdash.conf"
release_installer="$root/scripts/install-agent-release.sh"
enroller="$root/scripts/enroll-linux-agent.sh"
[[ -f "$service" && -f "$journal" && -f "$release_installer" && -f "$enroller" ]] ||
  die "copy the complete MinerDash bundle before running this installer"

install -d -o root -g root -m 0700 /etc/minerdash /var/lib/minerdash /opt/minerdash/miners
install -d -o root -g root -m 0755 /opt/minerdash/agent/releases /usr/local/sbin
install -d -o root -g root -m 0755 /etc/systemd/journald@minerdash.conf.d
install -o root -g root -m 0644 "$service" /etc/systemd/system/minerdash-agent.service
install -o root -g root -m 0644 "$journal" /etc/systemd/journald@minerdash.conf.d/60-retention.conf
install -o root -g root -m 0755 "$enroller" /usr/local/sbin/minerdash-enroll
bash "$release_installer" stage "$agent" "$agent_sha"
bash "$release_installer" activate "$agent_sha"
systemctl disable minerdash-agent.service >/dev/null 2>&1 || true
systemctl daemon-reload

echo "MinerDash agent installed but not enrolled or started."
echo "No existing mining service was stopped or removed."
echo "Next: inspect the rig, prepare a root-only token file, then run /usr/local/sbin/minerdash-enroll."
