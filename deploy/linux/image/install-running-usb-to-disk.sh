#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

die() { echo "$*" >&2; exit 1; }
usage() {
  echo "usage: $0 --target /dev/DISK --confirm ERASE-/dev/DISK" >&2
  echo "The target must be an unmounted, non-removable whole disk." >&2
  exit 2
}
[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run as root"
[[ -s /etc/minerdash/agent.json && -s /var/lib/minerdash/identity.json ]] ||
  die "enroll this rig before transferring its installation to an internal drive"
target= confirmation=
while (($#)); do
  case "$1" in
    --target) [[ $# -ge 2 ]] || usage; target=$2; shift 2 ;;
    --confirm) [[ $# -ge 2 ]] || usage; confirmation=$2; shift 2 ;;
    *) usage ;;
  esac
done
[[ "$target" == /dev/* && "$target" != *[[:space:]]* ]] || usage
target=$(readlink -f -- "$target")
[[ -b "$target" ]] || die "target is not a block device: $target"
[[ $(lsblk -dnro TYPE "$target") == disk ]] || die "target must be a whole disk"
[[ $(lsblk -dnro RM "$target") == 0 ]] || die "refusing a removable target disk"
[[ "$confirmation" == "ERASE-$target" ]] || die "confirmation must exactly equal ERASE-$target"

root_source=$(findmnt -nro SOURCE /)
[[ -b "$root_source" ]] || die "the running root filesystem is not on a directly traceable block device"
source_disk=$(readlink -f -- "$root_source")
while [[ $(lsblk -dnro TYPE "$source_disk") != disk ]]; do
  parent=$(lsblk -dnro PKNAME "$source_disk")
  [[ -n "$parent" ]] || die "cannot resolve the running USB parent disk"
  source_disk="/dev/$parent"
done
source_disk=$(readlink -f -- "$source_disk")
[[ "$target" != "$source_disk" ]] || die "refusing to overwrite the running system disk"

if lsblk -nrpo MOUNTPOINT "$target" | grep -q '[^[:space:]]'; then
  die "target or one of its partitions is mounted"
fi
if lsblk -nrpo TYPE "$target" | grep -Ev '^(disk|part)$' | grep -q .; then
  die "target participates in device-mapper, RAID, or another stacked block device"
fi
while read -r active_swap; do
  [[ -z "$active_swap" ]] && continue
  if lsblk -nrpo NAME "$target" | grep -Fxq "$active_swap"; then
    die "target contains active swap: $active_swap"
  fi
done < <(swapon --show=NAME --noheadings 2>/dev/null || true)
source_size=$(blockdev --getsize64 "$source_disk")
target_size=$(blockdev --getsize64 "$target")
((target_size >= source_size)) || die "target is smaller than the running USB disk"

systemctl stop minerdash-agent.service >/dev/null 2>&1 || true
sync
command -v fsfreeze >/dev/null || die "fsfreeze is required"
frozen=false
unfreeze() {
  if [[ "$frozen" == true ]]; then
    fsfreeze --unfreeze / >/dev/null 2>&1 || true
    frozen=false
  fi
}
trap unfreeze EXIT
fsfreeze --freeze /
frozen=true

echo "ERASING $target and cloning the complete running system from $source_disk" >&2
dd if="$source_disk" of="$target" bs=16M iflag=fullblock oflag=direct conv=fsync status=progress
unfreeze
trap - EXIT
sync
blockdev --rereadpt "$target" || true
udevadm settle

target_root=$(
  lsblk -bnrpo NAME,TYPE,FSTYPE,SIZE "$target" |
    awk '$2 == "part" && $3 ~ /^ext[234]$/ {print $4, $1}' |
    sort -nr | awk 'NR == 1 {print $2}'
)
[[ -n "$target_root" && -b "$target_root" ]] ||
  die "clone completed, but no ext root partition was found for identity cleanup"

mount_point="/run/minerdash-disk-install.$$"
install -d -o root -g root -m 0700 "$mount_point"
cleanup() {
  mountpoint -q "$mount_point" && umount "$mount_point"
  rmdir "$mount_point" 2>/dev/null || true
}
trap cleanup EXIT
mount "$target_root" "$mount_point"
[[ -d "$mount_point/etc" && -d "$mount_point/var/lib" ]] ||
  die "selected cloned partition is not a Linux root filesystem"
cmp -s /etc/minerdash/agent.json "$mount_point/etc/minerdash/agent.json" ||
  die "cloned target does not preserve the enrolled agent configuration"
cmp -s /var/lib/minerdash/identity.json "$mount_point/var/lib/minerdash/identity.json" ||
  die "cloned target does not preserve the enrolled rig identity"
sync
cleanup
trap - EXIT

rm -f -- /etc/minerdash/agent.json /var/lib/minerdash/identity.json
rm -f -- /etc/systemd/system/multi-user.target.wants/minerdash-agent.service
rm -f -- /etc/systemd/system/multi-user.target.wants/minerdash-first-boot.service
echo "Installed to $target. Shut down, remove the USB, and boot the target disk."
echo "The enrolled rig identity was transferred to the internal disk; this USB must be paired again before reuse."
echo "The running USB root remains read-only; reboot or power off now."
