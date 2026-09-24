#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

die() { echo "$*" >&2; exit 1; }
[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run as root"
action=${1:-plan}

echo "Detected display adapters:"
lspci -nn | grep -Ei 'VGA|3D|Display' || true
echo
echo "Ubuntu-supported recommendation (review only):"
ubuntu-drivers devices || true

case "$action" in
  plan)
    cat <<'EOF'
No driver was changed.
AMD: Ubuntu's in-kernel amdgpu driver is the default strategy. Do not add ROCm
or vendor repositories without separately pinning and reviewing their keys.
NVIDIA: review `ubuntu-drivers devices`, then invoke:
  setup-gpu-drivers.sh install-nvidia EXACT_PACKAGE EXACT_APT_VERSION
The package and version must exist in the already configured signed APT repos.
EOF
    ;;
  install-nvidia)
    [[ $# -eq 3 ]] || die "usage: $0 install-nvidia PACKAGE EXACT_APT_VERSION"
    package=$2 version=$3
    [[ "$package" =~ ^nvidia-driver-[0-9]+(-server)?$ ]] || die "unexpected NVIDIA package name"
    [[ "$version" =~ ^[A-Za-z0-9.+:~_-]+$ ]] || die "invalid APT version"
    candidate=$(apt-cache policy "$package" | awk '/Candidate:/ {print $2}')
    [[ "$candidate" != "(none)" && "$candidate" == "$version" ]] ||
      die "requested version is not the current signed APT candidate (candidate: $candidate)"
    export DEBIAN_FRONTEND=noninteractive
    apt-get install -y --no-install-recommends "$package=$version"
    echo "Installed the reviewed APT candidate. Reboot, then verify with nvidia-smi."
    ;;
  *) die "unknown action; use plan or install-nvidia" ;;
esac
