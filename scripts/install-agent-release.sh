#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

die() { echo "$*" >&2; exit 1; }
[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run as root"
[[ $# -ge 1 ]] || die "usage: $0 stage FILE SHA256 | activate SHA256 | rollback"
action=$1
releases=/opt/minerdash/agent/releases
current=/opt/minerdash/agent/current
previous=/opt/minerdash/agent/previous
install -d -o root -g root -m 0755 "$releases"

valid_digest() { [[ $1 =~ ^[0-9a-fA-F]{64}$ ]]; }
case "$action" in
  stage)
    [[ $# -eq 3 ]] || die "usage: $0 stage FILE SHA256"
    artifact=$2
    digest=${3,,}
    valid_digest "$digest" || die "invalid SHA-256"
    [[ -f "$artifact" && ! -L "$artifact" ]] || die "artifact must be a regular, non-symlink file"
    actual=$(sha256sum -- "$artifact" | awk '{print $1}')
    [[ "$actual" == "$digest" ]] || die "agent SHA-256 mismatch"
    destination="$releases/$digest"
    [[ ! -e "$destination" ]] || die "release already staged"
    install -d -o root -g root -m 0755 "$destination"
    install -o root -g root -m 0755 "$artifact" "$destination/minerdash-agent"
    "$destination/minerdash-agent" -h >/dev/null 2>&1 || {
      rm -rf -- "$destination"
      die "agent executable failed its startup argument check"
    }
    printf '%s  minerdash-agent\n' "$digest" >"$destination/SHA256SUMS"
    chmod 0644 "$destination/SHA256SUMS"
    echo "Staged $digest"
    ;;
  activate)
    [[ $# -eq 2 ]] || die "usage: $0 activate SHA256"
    digest=${2,,}; valid_digest "$digest" || die "invalid SHA-256"
    target="$releases/$digest"
    [[ -x "$target/minerdash-agent" ]] || die "release is not staged"
    (cd "$target" && sha256sum -c SHA256SUMS >/dev/null) || die "staged release failed verification"
    if [[ -L "$current" ]]; then
      ln -sfn -- "$(readlink -f "$current")" "$previous.new"
      mv -Tf -- "$previous.new" "$previous"
    fi
    ln -sfn -- "$target" "$current.new"
    mv -Tf -- "$current.new" "$current"
    systemctl daemon-reload
    if [[ -f /etc/minerdash/agent.json ]]; then
      systemctl restart minerdash-agent.service
    fi
    echo "Activated $digest"
    ;;
  rollback)
    [[ $# -eq 1 ]] || die "usage: $0 rollback"
    [[ -L "$previous" && -x "$previous/minerdash-agent" ]] || die "no valid previous release"
    old=$(readlink -f "$previous")
    now=$(readlink -f "$current")
    ln -sfn -- "$old" "$current.new"; mv -Tf -- "$current.new" "$current"
    ln -sfn -- "$now" "$previous.new"; mv -Tf -- "$previous.new" "$previous"
    systemctl restart minerdash-agent.service
    echo "Rolled back to $(basename "$old")"
    ;;
  *) die "unknown action: $action" ;;
esac
