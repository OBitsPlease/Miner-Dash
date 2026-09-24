#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

die() { echo "$*" >&2; exit 1; }
[[ $# -eq 2 ]] || die "usage: $0 INPUT.raw OUTPUT.img.xz"
input=$1
output=$2
[[ -f "$input" && ! -L "$input" ]] || die "input must be a regular raw image"
[[ "$input" == *.raw ]] || die "input must use the .raw suffix"
[[ "$output" == *.img.xz ]] || die "output must use the .img.xz suffix"
[[ ! -e "$output" && ! -e "$output.sha256" ]] || die "output already exists"
command -v xz >/dev/null || die "xz is required"

temporary="$output.upload"
trap 'rm -f -- "$temporary"' EXIT
xz --threads=0 --compress --stdout -- "$input" >"$temporary"
xz --test -- "$temporary"
mv -- "$temporary" "$output"
digest=$(sha256sum -- "$output" | awk '{print $1}')
printf '%s  %s\n' "$digest" "$(basename -- "$output")" >"$output.sha256"
chmod 0600 "$output" "$output.sha256"
trap - EXIT
echo "Packaged $output"
echo "SHA-256: $digest"
