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
    --output minerdash-usb.raw --size-gib N \
    [--miners-dir DIR --miners-manifest FILE]

The base image, agent, and optional miners are supplied by the operator.
Only the pinned XMRig Ubuntu package is retrieved through signature-verified
APT; no agent or proprietary miner is downloaded.
EOF
  exit 2
}

base= base_sha= agent= agent_sha= xmrig_version= xmrig_debs_dir= xmrig_debs_manifest=
output= size_gib=
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
    --output) [[ $# -ge 2 ]] || usage; output=$2; shift 2 ;;
    --size-gib) [[ $# -ge 2 ]] || usage; size_gib=$2; shift 2 ;;
    --miners-dir) [[ $# -ge 2 ]] || usage; miners_dir=$2; shift 2 ;;
    --miners-manifest) [[ $# -ge 2 ]] || usage; miners_manifest=$2; shift 2 ;;
    *) usage ;;
  esac
done

[[ -n "$base" && -n "$agent" && -n "$xmrig_version" &&
  -n "$xmrig_debs_dir" && -n "$xmrig_debs_manifest" &&
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
[[ ! -e "$output" ]] || die "output already exists: $output"
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
install -d -m 0755 "$payload/etc/systemd/system/getty@tty1.service.d"
install -d -m 0755 "$payload/etc/systemd/journald@minerdash.conf.d"
install -d -m 0755 "$payload/etc/netplan"
install -d -m 0755 "$payload/etc/profile.d"
install -d -m 0755 "$payload/etc/sudoers.d"
install -d -m 0700 "$payload/etc/minerdash"
install -d -m 0755 "$payload/opt/minerdash/agent/releases/$agent_sha"
install -d -m 0700 "$payload/opt/minerdash/miners"
install -d -m 0755 "$payload/usr/local/sbin" "$payload/usr/share/doc/minerdash-linux"
install -d -m 0755 "$payload/var/cache/minerdash/xmrig-debs"
install -m 0755 "$agent" "$payload/opt/minerdash/agent/releases/$agent_sha/minerdash-agent"
printf '%s  minerdash-agent\n' "$agent_sha" \
  >"$payload/opt/minerdash/agent/releases/$agent_sha/SHA256SUMS"
chmod 0644 "$payload/opt/minerdash/agent/releases/$agent_sha/SHA256SUMS"
install -m 0644 "$script_dir/../minerdash-agent.service" \
  "$payload/etc/systemd/system/minerdash-agent.service"
install -m 0644 "$script_dir/minerdash-first-boot.service" \
  "$payload/etc/systemd/system/minerdash-first-boot.service"
install -m 0644 "$script_dir/../journald-minerdash.conf" \
  "$payload/etc/systemd/journald@minerdash.conf.d/60-retention.conf"
install -m 0600 "$script_dir/01-minerdash-network.yaml" \
  "$payload/etc/netplan/01-minerdash-network.yaml"
install -m 0644 "$script_dir/../agent.json.template" \
  "$payload/etc/minerdash/agent.json.template"
install -m 0600 /dev/null "$payload/etc/minerdash/image-first-boot"
install -m 0755 "$script_dir/first-boot-identity.sh" \
  "$payload/usr/local/sbin/minerdash-first-boot-identity"
install -m 0755 "$script_dir/install-running-usb-to-disk.sh" \
  "$payload/usr/local/sbin/minerdash-install-to-disk"
install -m 0755 "$script_dir/console-enroll.sh" \
  "$payload/usr/local/sbin/minerdash-enroll-console"
install -m 0755 "$script_dir/console-menu.sh" \
  "$payload/usr/local/sbin/minerdash-console-menu"
install -m 0644 "$script_dir/minerdash-console-profile.sh" \
  "$payload/etc/profile.d/minerdash-console-menu.sh"
install -m 0755 "$enrollment_script" \
  "$payload/usr/local/sbin/minerdash-enroll"
install -m 0755 "$release_script" \
  "$payload/usr/local/sbin/minerdash-install-agent-release"
install -m 0755 "$script_dir/../agent-launcher.sh" \
  "$payload/usr/local/sbin/minerdash-agent-launcher"
install -m 0644 "$script_dir/minerdash-autologin.conf" \
  "$payload/etc/systemd/system/getty@tty1.service.d/autologin.conf"
install -m 0440 "$script_dir/minerdash-console-sudoers" \
  "$payload/etc/sudoers.d/minerdash-console"
install -m 0644 "$script_dir/../OPERATIONS.md" \
  "$payload/usr/share/doc/minerdash-linux/OPERATIONS.md"
ln -s /etc/systemd/system/minerdash-first-boot.service \
  "$payload/etc/systemd/system/multi-user.target.wants/minerdash-first-boot.service"

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
  --run-command "dpkg -i /var/cache/minerdash/xmrig-debs/*.deb" \
  --run-command "rm -rf /var/cache/minerdash/xmrig-debs" \
  --run-command "dpkg --purge cloud-init snapd" \
  --run-command "systemctl mask systemd-networkd-wait-online.service" \
  --run-command "command -v sudo >/dev/null" \
  --run-command "id minerdash >/dev/null 2>&1 || useradd --create-home --shell /bin/bash minerdash" \
  --run-command "passwd --lock minerdash" \
  --run-command "chown -R root:root /etc/minerdash /opt/minerdash /usr/local/sbin/minerdash-* /usr/share/doc/minerdash-linux" \
  --run-command "chmod 0440 /etc/sudoers.d/minerdash-console" \
  --run-command "chmod 0600 /etc/minerdash/image-first-boot" \
  --run-command "chmod 0644 /etc/systemd/system/minerdash-agent.service /etc/systemd/system/minerdash-first-boot.service /etc/systemd/system/getty@tty1.service.d/autologin.conf /etc/systemd/journald@minerdash.conf.d/60-retention.conf" \
  --run-command "chmod 0600 /etc/netplan/01-minerdash-network.yaml" \
  --run-command "netplan generate" \
  --run-command "chmod 0755 /usr/local/sbin/minerdash-enroll /usr/local/sbin/minerdash-enroll-console /usr/local/sbin/minerdash-console-menu /usr/local/sbin/minerdash-first-boot-identity /usr/local/sbin/minerdash-install-agent-release /usr/local/sbin/minerdash-agent-launcher /usr/local/sbin/minerdash-install-to-disk /opt/minerdash/agent/releases/$agent_sha/minerdash-agent" \
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

mv -- "$expanded" "$output"
output_sha=$(sha256sum -- "$output" | awk '{print $1}')
miners_sha=none
[[ -z "$miners_manifest" ]] || miners_sha=$(sha256sum -- "$miners_manifest" | awk '{print $1}')
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
