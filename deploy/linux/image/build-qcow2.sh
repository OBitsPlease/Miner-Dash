#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

usage() {
  echo "usage: $0 BASE_UBUNTU_24.04_QCOW2 EXPECTED_SHA256 OUTPUT_QCOW2" >&2
  exit 2
}

[[ $# -eq 3 ]] || usage
base=$1
expected=${2,,}
output=$3
[[ -f "$base" && ! -L "$base" ]] || { echo "base image must be a regular file" >&2; exit 1; }
[[ "$expected" =~ ^[0-9a-f]{64}$ ]] || { echo "invalid SHA-256" >&2; exit 2; }
[[ ! -e "$output" ]] || { echo "output already exists: $output" >&2; exit 1; }
command -v qemu-img >/dev/null || { echo "install qemu-utils from Ubuntu first" >&2; exit 1; }
command -v cloud-localds >/dev/null || { echo "install cloud-image-utils from Ubuntu first" >&2; exit 1; }

actual=$(sha256sum -- "$base" | awk '{print $1}')
[[ "$actual" == "$expected" ]] || { echo "base image SHA-256 mismatch" >&2; exit 1; }
format=$(qemu-img info --output=json "$base" | jq -r .format)
[[ "$format" == "qcow2" ]] || { echo "base image is not qcow2" >&2; exit 1; }

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
seed="${output%.*}-seed.iso"
[[ ! -e "$seed" ]] || { echo "seed output already exists: $seed" >&2; exit 1; }
cleanup() { rm -f -- "$output" "$seed"; }
trap cleanup ERR
qemu-img create -q -f qcow2 -F qcow2 -b "$(readlink -f "$base")" "$output"
cloud-localds "$seed" "$script_dir/user-data" "$script_dir/meta-data"
chmod 0600 "$output" "$seed"
trap - ERR
echo "Created $output and $seed. Attach both on first boot; no secret is included."
