#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
count=0
while IFS= read -r -d '' script; do
  bash -n "$script"
  ((count += 1))
done < <(find "$root/scripts" "$root/deploy/linux" -type f -name '*.sh' -print0)
echo "bash -n passed for $count Linux shell scripts"
