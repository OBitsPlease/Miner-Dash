#!/usr/bin/env bash
set -Eeuo pipefail

gpu_indices=$(nvidia-smi --query-gpu=index --format=csv,noheader,nounits)
[[ -n "$gpu_indices" ]] || {
  echo "No NVIDIA GPUs are available for headless Xorg" >&2
  exit 1
}

# NVIDIA compute initialization must not depend on a display server or
# nvidia-xconfig. Device nodes and persistence mode are prepared by the unit.
exit 0
