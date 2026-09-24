#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

die() { echo "$*" >&2; exit 1; }
usage() {
  echo "usage: $0 --plan | --apply --confirm EXPAND-/dev/PARTITION" >&2
  exit 2
}

mode= confirmation=
while (($#)); do
  case "$1" in
    --plan) mode=plan; shift ;;
    --apply) mode=apply; shift ;;
    --confirm) [[ $# -ge 2 ]] || usage; confirmation=$2; shift 2 ;;
    *) usage ;;
  esac
done
[[ "$mode" == plan || "$mode" == apply ]] || usage

root_source=$(findmnt -nro SOURCE /)
root_type=$(findmnt -nro FSTYPE /)
[[ "$root_source" == /dev/* && -b "$root_source" ]] ||
  die "root filesystem $root_source is not a directly supported block partition"
[[ $(lsblk -dnro TYPE "$root_source") == part ]] ||
  die "root filesystem must be a normal disk partition; overlay, LVM, RAID, and device-mapper roots require manual review"
parent_name=$(lsblk -dnro PKNAME "$root_source")
part_number=$(lsblk -dnro PARTN "$root_source")
[[ -n "$parent_name" && "$part_number" =~ ^[1-9][0-9]*$ ]] ||
  die "could not resolve the root partition's parent disk and partition number"
disk="/dev/$parent_name"
[[ -b "$disk" && $(lsblk -dnro TYPE "$disk") == disk ]] || die "resolved parent is not a whole disk"
[[ $(lsblk -dnro RO "$disk") == 0 ]] || die "root disk is read-only"
mount_options=$(findmnt -nro OPTIONS /)
[[ ",$mount_options," == *,rw,* ]] || die "root filesystem is not mounted read-write"

disk_size=$(blockdev --getsize64 "$disk")
partition_size=$(blockdev --getsize64 "$root_source")
free_bytes=$((disk_size - partition_size))
printf 'root_partition=%s\nparent_disk=%s\nfilesystem=%s\ndisk_bytes=%s\npartition_bytes=%s\npotential_growth_bytes=%s\n' \
  "$root_source" "$disk" "$root_type" "$disk_size" "$partition_size" "$free_bytes"

if [[ "$mode" == plan ]]; then
  echo "Plan only: no partition or filesystem changes were made."
  exit 0
fi

[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run --apply as root"
[[ "$confirmation" == "EXPAND-$root_source" ]] ||
  die "confirmation must exactly equal EXPAND-$root_source"
command -v growpart >/dev/null || die "growpart is required"
((free_bytes > 16 * 1024 * 1024)) || die "root partition already occupies the available disk"

growpart "$disk" "$part_number"
command -v partprobe >/dev/null && partprobe "$disk" || true
command -v udevadm >/dev/null && udevadm settle || true

case "$root_type" in
  ext2|ext3|ext4)
    command -v resize2fs >/dev/null || die "resize2fs is required for $root_type"
    resize2fs "$root_source"
    ;;
  xfs)
    command -v xfs_growfs >/dev/null || die "xfs_growfs is required"
    xfs_growfs /
    ;;
  btrfs)
    command -v btrfs >/dev/null || die "btrfs is required"
    btrfs filesystem resize max /
    ;;
  *)
    die "unsupported root filesystem: $root_type"
    ;;
esac

echo "Expansion complete."
df -hT /
