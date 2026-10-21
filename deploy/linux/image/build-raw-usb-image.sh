#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'
umask 077

die() { echo "$*" >&2; exit 1; }
usage() {
  cat >&2 <<'EOF'
usage:
  build-raw-usb-image.sh \
    --base ubuntu-24.04.qcow2 --base-sha256 HEX \
    --agent minerdash-agent --agent-sha256 HEX \
    --xmrig-version EXACT_UBUNTU_APT_VERSION \
    --xmrig-debs-dir DIR --xmrig-debs-manifest FILE \
    --network-debs-dir DIR --network-debs-manifest FILE \
    --output minerdash-usb.raw --size-gib N \
    [--nvidia-driver-package nvidia-driver-NNN \
      --nvidia-driver-version EXACT_APT_VERSION \
      --nvidia-driver-debs-dir DIR --nvidia-driver-debs-manifest FILE] \
    [--hardware-debs-dir DIR --hardware-debs-manifest FILE] \
    [--cpu-debs-dir DIR --cpu-debs-manifest FILE] \
    [--miners-dir DIR --miners-manifest FILE]

The base image, agent, and optional miners are supplied by the operator.
XMRig and kernel network modules are staged from Ubuntu's signature-verified
APT repositories; NVIDIA driver packages are also staged from signed Ubuntu
APT repositories when requested. No agent or proprietary miner is downloaded.
EOF
  exit 2
}

base= base_sha= agent= agent_sha= xmrig_version= xmrig_debs_dir= xmrig_debs_manifest=
network_debs_dir= network_debs_manifest=
nvidia_driver_package= nvidia_driver_version= nvidia_driver_debs_dir= nvidia_driver_debs_manifest=
hardware_debs_dir= hardware_debs_manifest=
cpu_debs_dir= cpu_debs_manifest=
miners_dir= miners_manifest=
while (($#)); do
  case "$1" in
    --base) [[ $# -ge 2 ]] || usage; base=$2; shift 2 ;;
    --base-sha256) [[ $# -ge 2 ]] || usage; base_sha=${2,,}; shift 2 ;;
    --agent) [[ $# -ge 2 ]] || usage; agent=$2; shift 2 ;;
    --agent-sha256) [[ $# -ge 2 ]] || usage; agent_sha=${2,,}; shift 2 ;;
    --xmrig-version) [[ $# -ge 2 ]] || usage; xmrig_version=$2; shift 2 ;;
    --xmrig-debs-dir) [[ $# -ge 2 ]] || usage; xmrig_debs_dir=$2; shift 2 ;;
    --xmrig-debs-manifest) [[ $# -ge 2 ]] || usage; xmrig_debs_manifest=$2; shift 2 ;;
    --network-debs-dir) [[ $# -ge 2 ]] || usage; network_debs_dir=$2; shift 2 ;;
    --network-debs-manifest) [[ $# -ge 2 ]] || usage; network_debs_manifest=$2; shift 2 ;;
    --nvidia-driver-package) [[ $# -ge 2 ]] || usage; nvidia_driver_package=$2; shift 2 ;;
    --nvidia-driver-version) [[ $# -ge 2 ]] || usage; nvidia_driver_version=$2; shift 2 ;;
    --nvidia-driver-debs-dir) [[ $# -ge 2 ]] || usage; nvidia_driver_debs_dir=$2; shift 2 ;;
    --nvidia-driver-debs-manifest) [[ $# -ge 2 ]] || usage; nvidia_driver_debs_manifest=$2; shift 2 ;;
    --hardware-debs-dir) [[ $# -ge 2 ]] || usage; hardware_debs_dir=$2; shift 2 ;;
    --hardware-debs-manifest) [[ $# -ge 2 ]] || usage; hardware_debs_manifest=$2; shift 2 ;;
    --cpu-debs-dir) [[ $# -ge 2 ]] || usage; cpu_debs_dir=$2; shift 2 ;;
    --cpu-debs-manifest) [[ $# -ge 2 ]] || usage; cpu_debs_manifest=$2; shift 2 ;;
    --output) [[ $# -ge 2 ]] || usage; output=$2; shift 2 ;;
    --size-gib) [[ $# -ge 2 ]] || usage; size_gib=$2; shift 2 ;;
    --miners-dir) [[ $# -ge 2 ]] || usage; miners_dir=$2; shift 2 ;;
    --miners-manifest) [[ $# -ge 2 ]] || usage; miners_manifest=$2; shift 2 ;;
    *) usage ;;
  esac
done

[[ -n "$base" && -n "$agent" && -n "$xmrig_version" &&
  -n "$xmrig_debs_dir" && -n "$xmrig_debs_manifest" &&
  -n "$network_debs_dir" && -n "$network_debs_manifest" &&
  -n "$output" && -n "$size_gib" ]] || usage
[[ "$base_sha" =~ ^[0-9a-f]{64}$ && "$agent_sha" =~ ^[0-9a-f]{64}$ ]] ||
  die "base and agent SHA-256 values must contain 64 hexadecimal digits"
[[ "$size_gib" =~ ^[1-9][0-9]*$ ]] || die "size-gib must be a positive integer"
[[ "$xmrig_version" =~ ^[A-Za-z0-9.+:~_-]+$ ]] || die "invalid XMRig APT version"
((size_gib >= 8 && size_gib <= 2048)) || die "size-gib must be between 8 and 2048"
[[ -f "$base" && ! -L "$base" ]] || die "base must be a regular, non-symlink file"
[[ -f "$agent" && ! -L "$agent" ]] || die "agent must be a regular, non-symlink file"
[[ -d "$xmrig_debs_dir" && -f "$xmrig_debs_manifest" && ! -L "$xmrig_debs_manifest" ]] ||
  die "XMRig deb directory and regular manifest are required"
xmrig_debs_dir=$(cd -- "$xmrig_debs_dir" && pwd -P)
[[ -d "$network_debs_dir" && -f "$network_debs_manifest" &&
  ! -L "$network_debs_manifest" ]] ||
  die "network deb directory and regular manifest are required"
network_debs_dir=$(cd -- "$network_debs_dir" && pwd -P)
if [[ -n "$nvidia_driver_package" || -n "$nvidia_driver_version" ||
  -n "$nvidia_driver_debs_dir" || -n "$nvidia_driver_debs_manifest" ]]; then
  [[ "$nvidia_driver_package" =~ ^nvidia-driver-[0-9]+$ ]] ||
    die "NVIDIA driver package must be an nvidia-driver-NNN package"
  [[ "$nvidia_driver_version" =~ ^[A-Za-z0-9.+:~_-]+$ ]] ||
    die "NVIDIA driver APT version is required"
  [[ -d "$nvidia_driver_debs_dir" && -f "$nvidia_driver_debs_manifest" &&
    ! -L "$nvidia_driver_debs_manifest" ]] ||
    die "NVIDIA driver deb directory and regular manifest are required"
  nvidia_driver_debs_dir=$(cd -- "$nvidia_driver_debs_dir" && pwd -P)
fi
if [[ -n "$hardware_debs_dir" || -n "$hardware_debs_manifest" ]]; then
  [[ -d "$hardware_debs_dir" && -f "$hardware_debs_manifest" && ! -L "$hardware_debs_manifest" ]] ||
    die "hardware package directory and regular manifest are required"
  hardware_debs_dir=$(cd -- "$hardware_debs_dir" && pwd -P)
fi
if [[ -n "$cpu_debs_dir" || -n "$cpu_debs_manifest" ]]; then
  [[ -d "$cpu_debs_dir" && -f "$cpu_debs_manifest" && ! -L "$cpu_debs_manifest" ]] ||
    die "CPU package directory and regular manifest are required"
  cpu_debs_dir=$(cd -- "$cpu_debs_dir" && pwd -P)
fi
[[ ! -e "$output" ]] || die "output already exists"
[[ "$output" == *.raw ]] || die "output must use the .raw suffix"
if [[ -n "$miners_dir" || -n "$miners_manifest" ]]; then
  [[ -d "$miners_dir" && -f "$miners_manifest" && ! -L "$miners_manifest" ]] ||
    die "both a miners directory and regular manifest are required"
  miners_dir=$(cd -- "$miners_dir" && pwd -P)
fi

for command in dpkg-deb jq qemu-img virt-customize virt-filesystems virt-resize sha256sum; do
  command -v "$command" >/dev/null || die "missing build dependency: $command"
done
[[ $(sha256sum -- "$base" | awk '{print $1}') == "$base_sha" ]] ||
  die "official base image SHA-256 mismatch"
[[ $(sha256sum -- "$agent" | awk '{print $1}') == "$agent_sha" ]] ||
  die "MinerDash agent SHA-256 mismatch"

base_info=$(qemu-img info --output=json "$base")
[[ $(jq -r .format <<<"$base_info") == qcow2 ]] || die "base image must be qcow2"
[[ $(jq -r '.["backing-filename"] // empty' <<<"$base_info") == "" ]] ||
  die "base qcow2 must not depend on a backing file"
virtual_size=$(jq -r '.["virtual-size"]' <<<"$base_info")
output_size=$((size_gib * 1024 * 1024 * 1024))
((output_size > virtual_size)) || die "requested raw image must be larger than the base virtual disk"

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
enrollment_script="$script_dir/../../../scripts/enroll-linux-agent.sh"
release_script="$script_dir/../../../scripts/install-agent-release.sh"
[[ -f "$enrollment_script" ]] || die "enrollment script not found in the source tree"
[[ -f "$release_script" ]] || die "agent release installer not found in the source tree"
output_dir=$(cd -- "$(dirname -- "$output")" && pwd -P)
output="$output_dir/$(basename -- "$output")"
build_manifest="$output.build-manifest"
[[ ! -e "$build_manifest" ]] || die "build manifest already exists: $build_manifest"
work=$(mktemp -d "$output_dir/.minerdash-image.XXXXXX")
payload="$work/payload"
expanded="$work/expanded.raw"
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

install -d -m 0755 "$payload/etc/systemd/system/multi-user.target.wants"
install -d -m 0755 "$payload/etc/systemd/journald@minerdash.conf.d"
install -d -m 0755 "$payload/etc/netplan"
install -d -m 0755 "$payload/etc/ssh/sshd_config.d"
install -d -m 0755 "$payload/etc/profile.d"
install -d -m 0755 "$payload/etc/sudoers.d"
install -d -m 0700 "$payload/etc/minerdash"
install -d -m 0755 "$payload/opt/minerdash/agent/releases/$agent_sha"
install -d -m 0700 "$payload/opt/minerdash/miners"
install -d -m 0755 "$payload/usr/local/sbin" "$payload/usr/share/doc/minerdash-linux"
install -d -m 0755 "$payload/var/cache/minerdash/xmrig-debs"
install -d -m 0755 "$payload/var/cache/minerdash/network-debs"
if [[ -n "$hardware_debs_dir" ]]; then install -d -m 0755 "$payload/var/cache/minerdash/hardware-debs"; fi
if [[ -n "$cpu_debs_dir" ]]; then install -d -m 0755 "$payload/var/cache/minerdash/cpu-debs"; fi
if [[ -n "$nvidia_driver_package" ]]; then
  install -d -m 0755 "$payload/var/cache/minerdash/nvidia-driver-debs"
  install -d -m 0755 "$payload/etc/systemd/system/minerdash-agent.service.d"
fi
install -m 0755 "$agent" "$payload/opt/minerdash/agent/releases/$agent_sha/minerdash-agent"
printf '%s  minerdash-agent\n' "$agent_sha" \
  >"$payload/opt/minerdash/agent/releases/$agent_sha/SHA256SUMS"
chmod 0644 "$payload/opt/minerdash/agent/releases/$agent_sha/SHA256SUMS"
install -m 0644 "$script_dir/../minerdash-agent.service" \
  "$payload/etc/systemd/system/minerdash-agent.service"
if [[ -n "$nvidia_driver_package" ]]; then
  install -m 0644 "$script_dir/../minerdash-nvidia-xorg.service" \
    "$payload/etc/systemd/system/minerdash-nvidia-xorg.service"
  install -m 0644 "$script_dir/../minerdash-agent-nvidia.conf" \
    "$payload/etc/systemd/system/minerdash-agent.service.d/20-nvidia-xorg.conf"
  install -m 0755 "$script_dir/../minerdash-nvidia-xorg-config.sh" \
    "$payload/usr/local/sbin/minerdash-nvidia-xorg-config"
  ln -s /etc/systemd/system/minerdash-nvidia-xorg.service \
    "$payload/etc/systemd/system/multi-user.target.wants/minerdash-nvidia-xorg.service"
fi
install -m 0644 "$script_dir/../minerdash-cpu-setup@.service" \
  "$payload/etc/systemd/system/minerdash-cpu-setup@.service"
install -m 0755 "$script_dir/../minerdash-cpu-setup.sh" \
  "$payload/usr/local/sbin/minerdash-cpu-setup"
install -m 0644 "$script_dir/minerdash-first-boot.service" \
  "$payload/etc/systemd/system/minerdash-first-boot.service"
install -m 0644 "$script_dir/../journald-minerdash.conf" \
  "$payload/etc/systemd/journald@minerdash.conf.d/60-retention.conf"
install -m 0600 "$script_dir/01-minerdash-network.yaml" \
  "$payload/etc/netplan/01-minerdash-network.yaml"
install -m 0644 "$script_dir/00-minerdash-ssh.conf" \
  "$payload/etc/ssh/sshd_config.d/00-minerdash.conf"
install -m 0644 "$script_dir/../agent.json.template" \
  "$payload/etc/minerdash/agent.json.template"
install -m 0600 /dev/null "$payload/etc/minerdash/image-first-boot"
install -m 0755 "$script_dir/first-boot-identity.sh" \
  "$payload/usr/local/sbin/minerdash-first-boot-identity"
install -m 0755 "$script_dir/install-running-usb-to-disk.sh" \
  "$payload/usr/local/sbin/minerdash-install-to-disk"
install -m 0755 "$script_dir/console-enroll.sh" \
  "$payload/usr/local/sbin/minerdash-enroll-console"
install -m 0755 "$enrollment_script" \
  "$payload/usr/local/sbin/minerdash-enroll"
install -m 0755 "$release_script" \
  "$payload/usr/local/sbin/minerdash-install-agent-release"
install -m 0755 "$script_dir/../agent-launcher.sh" \
  "$payload/usr/local/sbin/minerdash-agent-launcher"
install -m 0440 "$script_dir/minerdash-console-sudoers" \
  "$payload/etc/sudoers.d/minerdash-console"
install -m 0644 "$script_dir/../OPERATIONS.md" \
  "$payload/usr/share/doc/minerdash-linux/OPERATIONS.md"
ln -s /etc/systemd/system/minerdash-first-boot.service \
  "$payload/etc/systemd/system/multi-user.target.wants/minerdash-first-boot.service"
# Octominer fan boards reset the motherboard every 25 minutes unless fed (HiveOS feeds it too).
install -m 0755 "$script_dir/../octofan-watchdog.py" \
  "$payload/usr/local/sbin/octofan-watchdog.py"
install -m 0644 "$script_dir/../octofan-watchdog.service" \
  "$payload/etc/systemd/system/octofan-watchdog.service"
ln -s /etc/systemd/system/octofan-watchdog.service \
  "$payload/etc/systemd/system/multi-user.target.wants/octofan-watchdog.service"

xmrig_package_found=false
declare -A seen_xmrig_packages=()
while IFS=' ' read -r digest relative extra; do
  [[ -z "${digest:-}" || "$digest" == \#* ]] && continue
  [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-fA-F]{64}$ ]] ||
    die "invalid XMRig package manifest entry"
  [[ "$relative" =~ ^[A-Za-z0-9.+_~-]+\.deb$ ]] ||
    die "unsafe XMRig package name: $relative"
  source_file="$xmrig_debs_dir/$relative"
  [[ -f "$source_file" && ! -L "$source_file" ]] ||
    die "XMRig package is not a regular file: $relative"
  [[ $(sha256sum -- "$source_file" | awk '{print $1}') == "${digest,,}" ]] ||
    die "XMRig package SHA-256 mismatch: $relative"
  package_name=$(dpkg-deb --field "$source_file" Package)
  package_version=$(dpkg-deb --field "$source_file" Version)
  case "$package_name" in
    xmrig)
      [[ "$package_version" == "$xmrig_version" ]] ||
        die "XMRig package version does not match --xmrig-version"
      xmrig_package_found=true
      ;;
    libfmt9|libhwloc15) ;;
    *) die "unexpected package in XMRig offline bundle: $package_name" ;;
  esac
  [[ -z "${seen_xmrig_packages[$package_name]:-}" ]] ||
    die "duplicate XMRig bundle package: $package_name"
  seen_xmrig_packages[$package_name]=1
  install -m 0644 "$source_file" "$payload/var/cache/minerdash/xmrig-debs/$relative"
done <"$xmrig_debs_manifest"
[[ "$xmrig_package_found" == true ]] || die "offline bundle does not contain XMRig"
[[ -n "${seen_xmrig_packages[libfmt9]:-}" && -n "${seen_xmrig_packages[libhwloc15]:-}" ]] ||
  die "offline bundle must contain libfmt9 and libhwloc15"
xmrig_debs_sha=$(sha256sum -- "$xmrig_debs_manifest" | awk '{print $1}')

network_modules_found=false
wireless_regdb_found=false
network_kernel_release=
declare -A seen_network_packages=()
while IFS=' ' read -r digest relative extra; do
  [[ -z "${digest:-}" || "$digest" == \#* ]] && continue
  [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-fA-F]{64}$ ]] ||
    die "invalid network package manifest entry"
  [[ "$relative" =~ ^[A-Za-z0-9.+_~-]+\.deb$ ]] ||
    die "unsafe network package name: $relative"
  source_file="$network_debs_dir/$relative"
  [[ -f "$source_file" && ! -L "$source_file" ]] ||
    die "network package is not a regular file: $relative"
  [[ $(sha256sum -- "$source_file" | awk '{print $1}') == "${digest,,}" ]] ||
    die "network package SHA-256 mismatch: $relative"
  package_name=$(dpkg-deb --field "$source_file" Package)
  case "$package_name" in
    linux-modules-extra-*)
      network_kernel_release=${package_name#linux-modules-extra-}
      [[ "$network_kernel_release" =~ ^[0-9][A-Za-z0-9.+~-]*$ ]] ||
        die "invalid kernel release in network modules package: $package_name"
      network_modules_found=true
      ;;
    wireless-regdb)
      wireless_regdb_found=true
      ;;
    *) die "unexpected package in network driver bundle: $package_name" ;;
  esac
  [[ -z "${seen_network_packages[$package_name]:-}" ]] ||
    die "duplicate network driver package: $package_name"
  seen_network_packages[$package_name]=1
  install -m 0644 "$source_file" "$payload/var/cache/minerdash/network-debs/$relative"
done <"$network_debs_manifest"
[[ "$network_modules_found" == true ]] ||
  die "network driver bundle must contain one linux-modules-extra package"
[[ "$wireless_regdb_found" == true ]] ||
  die "network driver bundle must contain wireless-regdb"
network_debs_sha=$(sha256sum -- "$network_debs_manifest" | awk '{print $1}')

hardware_debs_sha=none
if [[ -n "$hardware_debs_dir" ]]; then
  while IFS=' ' read -r digest relative extra; do
    [[ -z "${digest:-}" || "$digest" == \#* ]] && continue
    [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-fA-F]{64}$ ]] || die "invalid hardware package manifest entry"
    [[ "$relative" =~ ^[A-Za-z0-9.%+_~-]+\.deb$ ]] || die "unsafe hardware package name: $relative"
    source_file="$hardware_debs_dir/$relative"
    [[ -f "$source_file" && ! -L "$source_file" ]] || die "hardware package is not regular: $relative"
    [[ $(sha256sum -- "$source_file" | awk '{print $1}') == "${digest,,}" ]] || die "hardware package SHA-256 mismatch: $relative"
    install -m 0644 "$source_file" "$payload/var/cache/minerdash/hardware-debs/$relative"
  done < "$hardware_debs_manifest"
  hardware_debs_sha=$(sha256sum -- "$hardware_debs_manifest" | awk '{print $1}')
fi
cpu_debs_sha=none
if [[ -n "$cpu_debs_dir" ]]; then
  while IFS=' ' read -r digest relative extra; do
    [[ -z "${digest:-}" || "$digest" == \#* ]] && continue
    [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-fA-F]{64}$ ]] || die "invalid CPU package manifest entry"
    [[ "$relative" =~ ^[A-Za-z0-9.%+_~-]+\.deb$ ]] || die "unsafe CPU package name: $relative"
    source_file="$cpu_debs_dir/$relative"
    [[ -f "$source_file" && ! -L "$source_file" ]] || die "CPU package is not regular: $relative"
    [[ $(sha256sum -- "$source_file" | awk '{print $1}') == "${digest,,}" ]] || die "CPU package SHA-256 mismatch: $relative"
    install -m 0644 "$source_file" "$payload/var/cache/minerdash/cpu-debs/$relative"
  done < "$cpu_debs_manifest"
  cpu_debs_sha=$(sha256sum -- "$cpu_debs_manifest" | awk '{print $1}')
fi

nvidia_driver_debs_sha=none
if [[ -n "$nvidia_driver_package" ]]; then
  nvidia_driver_package_found=false
  ubuntu_drivers_found=false
  nvidia_settings_found=false
  nvidia_compute_utils_found=false
  nvidia_utils_found=false
  libnvidia_compute_found=false
  nvidia_kernel_common_found=false
  nvidia_modprobe_found=false
  nvidia_dkms_found=false
  xorg_found=false
  nvidia_xconfig_found=false
  declare -A seen_nvidia_packages=()
  while IFS=' ' read -r digest relative extra; do
    [[ -z "${digest:-}" || "$digest" == \#* ]] && continue
    [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-fA-F]{64}$ ]] ||
      die "invalid NVIDIA driver package manifest entry"
    [[ "$relative" =~ ^[A-Za-z0-9.%+_~-]+\.deb$ ]] ||
      die "unsafe NVIDIA driver package name: $relative"
    source_file="$nvidia_driver_debs_dir/$relative"
    [[ -f "$source_file" && ! -L "$source_file" ]] ||
      die "NVIDIA driver package is not a regular file: $relative"
    [[ $(sha256sum -- "$source_file" | awk '{print $1}') == "${digest,,}" ]] ||
      die "NVIDIA driver package SHA-256 mismatch: $relative"
    package_name=$(dpkg-deb --field "$source_file" Package)
    package_version=$(dpkg-deb --field "$source_file" Version)
    package_architecture=$(dpkg-deb --field "$source_file" Architecture)
    [[ "$package_name" =~ ^[a-z0-9][a-z0-9+.-]*$ ]] ||
      die "invalid NVIDIA driver package name: $package_name"
    [[ "$package_architecture" == amd64 || "$package_architecture" == all ]] ||
      die "NVIDIA driver package must target amd64 or all: $relative"
    [[ -z "${seen_nvidia_packages[$package_name]:-}" ]] ||
      die "duplicate NVIDIA driver bundle package: $package_name"
    seen_nvidia_packages[$package_name]=1
    case "$package_name" in
      "$nvidia_driver_package")
        [[ "$package_version" == "$nvidia_driver_version" ]] ||
          die "NVIDIA driver package version does not match --nvidia-driver-version"
        nvidia_driver_package_found=true
        ;;
      ubuntu-drivers-common) ubuntu_drivers_found=true ;;
      nvidia-settings) nvidia_settings_found=true ;;
      nvidia-compute-utils-*) nvidia_compute_utils_found=true ;;
      nvidia-utils-*) nvidia_utils_found=true ;;
      libnvidia-compute-*) libnvidia_compute_found=true ;;
      nvidia-kernel-common-*) nvidia_kernel_common_found=true ;;
      nvidia-modprobe) nvidia_modprobe_found=true ;;
      nvidia-xconfig) nvidia_xconfig_found=true ;;
      xserver-xorg-core) xorg_found=true ;;
      nvidia-dkms-*)
        [[ "$package_name" == "nvidia-dkms-${nvidia_driver_package#nvidia-driver-}" ]] ||
          die "NVIDIA DKMS package does not match the selected driver series"
        [[ "$package_version" == "$nvidia_driver_version" ]] ||
          die "NVIDIA DKMS package version does not match the selected driver version"
        nvidia_dkms_found=true
        ;;
      nvidia-driver-*)
        die "unexpected NVIDIA driver series in bundle: $package_name"
        ;;
    esac
    install -m 0644 "$source_file" \
      "$payload/var/cache/minerdash/nvidia-driver-debs/$relative"
  done <"$nvidia_driver_debs_manifest"
  [[ "$nvidia_driver_package_found" == true ]] ||
    die "NVIDIA driver bundle does not contain $nvidia_driver_package"
  [[ "$ubuntu_drivers_found" == true && "$nvidia_settings_found" == true &&
    "$nvidia_compute_utils_found" == true && "$nvidia_utils_found" == true &&
    "$libnvidia_compute_found" == true && "$nvidia_kernel_common_found" == true &&
    "$nvidia_modprobe_found" == true && "$nvidia_dkms_found" == true &&
    "$xorg_found" == true ]] ||
    die "NVIDIA bundle must include compute utilities, CUDA library, kernel common files, DKMS, nvidia-settings, and Xorg; this is required for GeForce, RTX, Tesla, and CMP headless mining cards"
  nvidia_driver_debs_sha=$(sha256sum -- "$nvidia_driver_debs_manifest" | awk '{print $1}')
fi

miner_count=0
if [[ -n "$miners_manifest" ]]; then
  declare -A seen_paths=()
  verified_manifest="$payload/opt/minerdash/miners/SHA256SUMS.operator"
  : >"$verified_manifest"
  while IFS=' ' read -r digest relative extra; do
    [[ -z "${digest:-}" || "$digest" == \#* ]] && continue
    [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-fA-F]{64}$ ]] ||
      die "invalid miner manifest entry"
    [[ "$relative" =~ ^[A-Za-z0-9._/-]+$ && "$relative" != /* &&
      "$relative" != *"/../"* && "$relative" != ../* && "$relative" != *"/.." &&
      "$relative" != */./* && "$relative" != ./* ]] || die "unsafe miner path: $relative"
    [[ -z "${seen_paths[$relative]:-}" ]] || die "duplicate miner path: $relative"
    seen_paths[$relative]=1
    source_file="$miners_dir/$relative"
    [[ -f "$source_file" && ! -L "$source_file" ]] ||
      die "miner is not a regular, non-symlink file: $relative"
    source_real=$(readlink -f -- "$source_file")
    [[ "$source_real" == "$miners_dir/"* ]] || die "miner path escapes supplied directory: $relative"
    digest=${digest,,}
    [[ $(sha256sum -- "$source_file" | awk '{print $1}') == "$digest" ]] ||
      die "miner SHA-256 mismatch: $relative"
    install -d -m 0700 "$payload/opt/minerdash/miners/$(dirname -- "$relative")"
    install -m 0755 "$source_file" "$payload/opt/minerdash/miners/$relative"
    printf '%s  %s\n' "$digest" "$relative" >>"$verified_manifest"
    ((miner_count += 1))
  done <"$miners_manifest"
  ((miner_count > 0)) || die "miner manifest contained no binaries"
  chmod 0600 "$verified_manifest"
fi

mapfile -t filesystems < <(
  virt-filesystems --filesystems --long --all -a "$base" |
    awk 'NR > 1 && $3 ~ /^ext[234]$/ {print $1, $5}' | sort -k2,2nr
)
((${#filesystems[@]} > 0)) || die "no supported ext root filesystem found in base image"
root_partition=${filesystems[0]%% *}

qemu-img create -q -f raw "$expanded" "$output_size"
virt-resize --expand "$root_partition" "$base" "$expanded"
virt-customize --format raw -a "$expanded" --no-logfile \
  --copy-in "$payload/etc:/"
virt-customize --format raw -a "$expanded" --no-logfile \
  --copy-in "$payload/opt:/" --copy-in "$payload/usr:/" --copy-in "$payload/var:/" \
  --run-command "grep -Eq '^VERSION_ID=\"?24\\.04\"?$' /etc/os-release" \
  --run-command "test -d /lib/modules/$network_kernel_release" \
  --run-command "dpkg -i /var/cache/minerdash/network-debs/*.deb" \
  --run-command "depmod -a '$network_kernel_release'" \
  --run-command "test -e /lib/modules/$network_kernel_release/kernel/drivers/net/ethernet/realtek/r8169.ko -o -e /lib/modules/$network_kernel_release/kernel/drivers/net/ethernet/realtek/r8169.ko.zst" \
  --run-command "rm -rf /var/cache/minerdash/network-debs" \
  --run-command "dpkg -i /var/cache/minerdash/xmrig-debs/*.deb" \
  --run-command "rm -rf /var/cache/minerdash/xmrig-debs" \
  --run-command "if compgen -G '/var/cache/minerdash/hardware-debs/*.deb' > /dev/null; then dpkg -i /var/cache/minerdash/hardware-debs/*.deb || true; dpkg --configure -a; fi" \
  --run-command "if compgen -G '/var/cache/minerdash/cpu-debs/*.deb' > /dev/null; then dpkg -i /var/cache/minerdash/cpu-debs/*.deb || true; dpkg --configure -a; fi" \
  --run-command "rm -rf /var/cache/minerdash/hardware-debs /var/cache/minerdash/cpu-debs" \
  --run-command "dpkg --purge cloud-init snapd" \
  --run-command "chmod 0755 /etc /etc/ssh /etc/ssh/sshd_config.d /etc/systemd/system /etc/netplan && chmod 0750 /etc/sudoers.d" \
  --run-command "chmod 0644 /etc/systemd/system/minerdash-agent.service /etc/systemd/system/minerdash-cpu-setup@.service /etc/systemd/system/minerdash-first-boot.service /etc/systemd/system/octofan-watchdog.service /etc/systemd/journald@minerdash.conf.d/60-retention.conf /etc/ssh/sshd_config.d/00-minerdash.conf && chmod 0440 /etc/sudoers.d/minerdash-console" \
  --run-command "systemctl unmask systemd-networkd-wait-online.service" \
  --run-command "systemctl enable systemd-networkd-wait-online.service" \
  --run-command "systemctl mask getty@tty1.service" \
  --run-command "systemctl enable ssh.socket" \
  --run-command "ufw allow OpenSSH" \
  --run-command "command -v sudo >/dev/null" \
  --run-command "id minerdash >/dev/null 2>&1 || useradd --create-home --shell /bin/bash minerdash" \
  --run-command "passwd --lock minerdash" \
  --run-command "id user >/dev/null 2>&1 || useradd --create-home --shell /bin/bash --groups sudo user" \
  --run-command "usermod --append --groups sudo user" \
  --run-command "printf 'user:1\n' | chpasswd" \
  --run-command "install -d -m 0755 /run/sshd && ssh-keygen -A && sshd -t" \
  --run-command "chown -R root:root /etc/minerdash /opt/minerdash /usr/local/sbin/minerdash-* /usr/share/doc/minerdash-linux" \
  --run-command "chmod 0600 /etc/minerdash/image-first-boot" \
  --run-command "chmod 0600 /etc/netplan/01-minerdash-network.yaml" \
  --run-command "netplan generate" \
  --run-command "chmod 0755 /usr/local/sbin/minerdash-enroll /usr/local/sbin/minerdash-enroll-console /usr/local/sbin/minerdash-first-boot-identity /usr/local/sbin/minerdash-install-agent-release /usr/local/sbin/minerdash-agent-launcher /usr/local/sbin/minerdash-install-to-disk /usr/local/sbin/minerdash-cpu-setup /opt/minerdash/agent/releases/$agent_sha/minerdash-agent" \
  --run-command "visudo --check --file=/etc/sudoers.d/minerdash-console" \
  --run-command "test \"\$(dpkg-query -W -f='\${Version}' xmrig)\" = '$xmrig_version'" \
  --run-command "test -x /usr/bin/xmrig" \
  --run-command "/opt/minerdash/agent/releases/$agent_sha/minerdash-agent -h >/dev/null" \
  --run-command "ln -sfn /opt/minerdash/agent/releases/$agent_sha /opt/minerdash/agent/current" \
  --run-command "install -d -o root -g root -m 0700 /var/lib/minerdash /opt/minerdash/miners" \
  --run-command "grub-install --target=i386-pc /dev/sda" \
  --run-command "update-grub" \
  --run-command "rm -f /etc/machine-id /var/lib/dbus/machine-id /var/lib/systemd/random-seed" \
  --run-command "touch /etc/machine-id" \
  --run-command "rm -f /etc/ssh/ssh_host_*" \
  --run-command "rm -f /etc/minerdash/agent.json /var/lib/minerdash/identity.json" \
  --run-command "printf '%s\n' minerdash-template > /etc/hostname"

if [[ -n "$nvidia_driver_package" ]]; then
  virt-customize --format raw -a "$expanded" --no-logfile \
    --run-command "export DEBIAN_FRONTEND=noninteractive; dpkg -i /var/cache/minerdash/nvidia-driver-debs/*.deb > /var/log/minerdash-image-nvidia-driver-install.log 2>&1 || true; dpkg --configure -a >> /var/log/minerdash-image-nvidia-driver-install.log 2>&1; test -z \"\$(dpkg --audit)\"" \
    --run-command "test \"\$(dpkg-query -W -f='\${Version}' '$nvidia_driver_package')\" = '$nvidia_driver_version'" \
    --run-command "test -x /usr/bin/ubuntu-drivers && test -x /usr/bin/nvidia-smi && test -x /usr/bin/nvidia-modprobe && test -x /usr/bin/nvidia-settings && test -x /usr/bin/Xorg" \
    --run-command "modinfo -k '$network_kernel_release' nvidia >/dev/null" \
    --run-command "rm -rf /var/cache/minerdash/nvidia-driver-debs" \
    --run-command "apt-get clean"
fi

mv -- "$expanded" "$output"
output_sha=$(sha256sum -- "$output" | awk '{print $1}')
miners_sha=none
[[ -z "$miners_manifest" ]] || miners_sha=$(sha256sum -- "$miners_manifest" | awk '{print $1}')
nvidia_driver_package=${nvidia_driver_package:-none}
nvidia_driver_version=${nvidia_driver_version:-none}
nvidia_driver_kernel_release=none
nvidia_cmp_support=none
[[ "$nvidia_driver_package" == none ]] || { nvidia_driver_kernel_release=$network_kernel_release; nvidia_cmp_support=driver-stack-validated; }
qemu_version=$(qemu-img --version)
qemu_version=${qemu_version%%$'\n'*}
resize_version=$(virt-resize --version)
customize_version=$(virt-customize --version)
cat >"$build_manifest" <<EOF
format=minerdash-raw-usb-v1
ubuntu_base_sha256=$base_sha
agent_sha256=$agent_sha
xmrig_ubuntu_apt_version=$xmrig_version
xmrig_debs_manifest_sha256=$xmrig_debs_sha
network_debs_manifest_sha256=$network_debs_sha
network_kernel_release=$network_kernel_release
hardware_debs_manifest_sha256=$hardware_debs_sha
cpu_debs_manifest_sha256=$cpu_debs_sha
nvidia_driver_package=$nvidia_driver_package
nvidia_driver_version=$nvidia_driver_version
nvidia_driver_kernel_release=$nvidia_driver_kernel_release
nvidia_cmp_support=$nvidia_cmp_support
nvidia_driver_debs_manifest_sha256=$nvidia_driver_debs_sha
miners_manifest_sha256=$miners_sha
miner_count=$miner_count
size_bytes=$output_size
root_partition=$root_partition
qemu_img_version=$qemu_version
virt_resize_version=$resize_version
virt_customize_version=$customize_version
output_sha256=$output_sha
EOF
chmod 0600 "$output" "$build_manifest"
trap - EXIT
cleanup
echo "Created verified bootable raw USB image: $output"
echo "Output SHA-256: $output_sha"
echo "Audit manifest: $build_manifest"
