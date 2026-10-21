#!/usr/bin/env bash
# Builds the Miner Dash controller installers for Linux (.deb and .tar.gz) and
# macOS (.pkg). Run it on the matching operating system; packaging tools such as
# dpkg-deb and pkgbuild are only available on their native platform.
set -Eeuo pipefail
IFS=$'\n\t'

usage() {
  echo "usage: $0 --target {linux|macos} --version X.Y.Z [--output DIR]" >&2
  exit 2
}

target=""
version=""
output=""
while (($# > 0)); do
  case "$1" in
    --target) target=${2:-}; shift 2 ;;
    --version) version=${2:-}; shift 2 ;;
    --output) output=${2:-}; shift 2 ;;
    *) usage ;;
  esac
done
[[ $target == linux || $target == macos ]] || usage
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version must look like 1.0.3" >&2; exit 2; }

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
output=${output:-$root/build}
mkdir -p "$output"
staging="$output/package-staging-$target"
rm -rf "$staging"
mkdir -p "$staging"

linker_flags="-s -w -X minerdash/internal/buildinfo.Version=$version"

build_binary() {
  local goos=$1 goarch=$2 package=$3 destination=$4
  mkdir -p "$(dirname -- "$destination")"
  (cd "$root" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "$linker_flags" -o "$destination" "./cmd/$package")
}

checksum() {
  local file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$(dirname -- "$file")" && sha256sum "$(basename -- "$file")" >"$(basename -- "$file").sha256")
  else
    (cd "$(dirname -- "$file")" && shasum -a 256 "$(basename -- "$file")" >"$(basename -- "$file").sha256")
  fi
}

build_linux_deb() {
  local arch=$1 goarch=$2
  local tree="$staging/deb-$arch"
  rm -rf "$tree"
  mkdir -p "$tree/DEBIAN" "$tree/opt/minerdash/agent-releases" "$tree/lib/systemd/system" "$tree/usr/share/doc/minerdash-controller"
  build_binary linux "$goarch" minerdash-server "$tree/opt/minerdash/minerdash-server"
  chmod 0755 "$tree/opt/minerdash/minerdash-server"
  build_binary linux amd64 minerdash-agent "$tree/opt/minerdash/agent-releases/minerdash-agent-amd64"
  build_binary linux arm64 minerdash-agent "$tree/opt/minerdash/agent-releases/minerdash-agent-arm64"
  chmod 0755 "$tree/opt/minerdash/agent-releases/minerdash-agent-amd64" "$tree/opt/minerdash/agent-releases/minerdash-agent-arm64"
  cp "$root/README.md" "$tree/usr/share/doc/minerdash-controller/README.md"
  cat >"$tree/DEBIAN/control" <<EOF
Package: minerdash-controller
Version: $version
Section: utils
Priority: optional
Architecture: $arch
Maintainer: OBitsPlease <OBitsPlease@users.noreply.github.com>
Depends: ca-certificates
Description: Miner Dash controller
 Dashboard and control plane for Miner Dash mining rigs. Serves the web
 dashboard over HTTPS on port 8443 and manages enrolled rig agents.
EOF
  cat >"$tree/lib/systemd/system/minerdash-controller.service" <<'EOF'
[Unit]
Description=Miner Dash controller
Documentation=https://github.com/OBitsPlease/Miner-Dash
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=/opt/minerdash/minerdash-server -listen :8443 -data /var/lib/minerdash-controller
WorkingDirectory=/var/lib/minerdash-controller
Restart=on-failure
RestartSec=5
UMask=0077
NoNewPrivileges=true
ProtectSystem=full
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
  cat >"$tree/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
install -d -m 0700 /var/lib/minerdash-controller /var/lib/minerdash-controller/agent-releases
for architecture in amd64 arm64; do
  install -m 0700 "/opt/minerdash/agent-releases/minerdash-agent-$architecture" \
    "/var/lib/minerdash-controller/agent-releases/minerdash-agent-$architecture"
done
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload
  systemctl enable minerdash-controller.service
  systemctl restart minerdash-controller.service
fi
echo "Miner Dash controller is listening on https://<this-host>:8443"
EOF
  cat >"$tree/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  systemctl disable --now minerdash-controller.service || true
fi
EOF
  chmod 0755 "$tree/DEBIAN/postinst" "$tree/DEBIAN/prerm"
  local package="$output/minerdash-controller_${version}_${arch}.deb"
  dpkg-deb --build --root-owner-group "$tree" "$package" >/dev/null
  checksum "$package"
  echo "built $package"
}

build_linux_tarball() {
  local tree="$staging/tarball"
  rm -rf "$tree"
  mkdir -p "$tree/bin"
  build_binary linux amd64 minerdash-server "$tree/bin/minerdash-server-amd64"
  build_binary linux arm64 minerdash-server "$tree/bin/minerdash-server-arm64"
  build_binary linux amd64 minerdash-agent "$tree/bin/minerdash-agent-amd64"
  build_binary linux arm64 minerdash-agent "$tree/bin/minerdash-agent-arm64"
  cp "$root/README.md" "$tree/README.md"
  cat >"$tree/install.sh" <<'EOF'
#!/bin/sh
# Installs the Miner Dash controller on any systemd Linux distribution.
set -e
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
here=$(cd -- "$(dirname -- "$0")" && pwd -P)
case "$(uname -m)" in
  x86_64|amd64) binary="$here/bin/minerdash-server-amd64" ;;
  aarch64|arm64) binary="$here/bin/minerdash-server-arm64" ;;
  *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
install -d -m 0755 /opt/minerdash
install -m 0755 "$binary" /opt/minerdash/minerdash-server
install -d -m 0700 /var/lib/minerdash-controller /var/lib/minerdash-controller/agent-releases
install -m 0700 "$here/bin/minerdash-agent-amd64" /var/lib/minerdash-controller/agent-releases/minerdash-agent-amd64
install -m 0700 "$here/bin/minerdash-agent-arm64" /var/lib/minerdash-controller/agent-releases/minerdash-agent-arm64
cat >/etc/systemd/system/minerdash-controller.service <<'UNIT'
[Unit]
Description=Miner Dash controller
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStart=/opt/minerdash/minerdash-server -listen :8443 -data /var/lib/minerdash-controller
WorkingDirectory=/var/lib/minerdash-controller
Restart=on-failure
RestartSec=5
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now minerdash-controller.service
echo "Miner Dash controller is listening on https://<this-host>:8443"
EOF
  chmod 0755 "$tree/install.sh"
  local archive="$output/minerdash-controller-${version}-linux.tar.gz"
  tar -czf "$archive" -C "$tree" .
  checksum "$archive"
  echo "built $archive"
}

build_macos_pkg() {
  local payload="$staging/payload"
  local scripts="$staging/scripts"
  rm -rf "$payload" "$scripts"
  mkdir -p "$payload/usr/local/minerdash/agent-releases" "$payload/Library/LaunchDaemons" "$scripts"
  build_binary darwin amd64 minerdash-server "$staging/minerdash-server-amd64"
  build_binary darwin arm64 minerdash-server "$staging/minerdash-server-arm64"
  lipo -create -output "$payload/usr/local/minerdash/minerdash-server" \
    "$staging/minerdash-server-amd64" "$staging/minerdash-server-arm64"
  chmod 0755 "$payload/usr/local/minerdash/minerdash-server"
  build_binary linux amd64 minerdash-agent "$payload/usr/local/minerdash/agent-releases/minerdash-agent-amd64"
  build_binary linux arm64 minerdash-agent "$payload/usr/local/minerdash/agent-releases/minerdash-agent-arm64"
  cat >"$payload/Library/LaunchDaemons/com.minerdash.controller.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.minerdash.controller</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/minerdash/minerdash-server</string>
    <string>-listen</string>
    <string>:8443</string>
    <string>-data</string>
    <string>/usr/local/var/minerdash-controller</string>
  </array>
  <key>WorkingDirectory</key><string>/usr/local/var/minerdash-controller</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/usr/local/var/log/minerdash-controller.log</string>
  <key>StandardErrorPath</key><string>/usr/local/var/log/minerdash-controller.log</string>
</dict>
</plist>
EOF
  cat >"$scripts/postinstall" <<'EOF'
#!/bin/sh
set -e
install -d -m 0700 /usr/local/var/minerdash-controller /usr/local/var/minerdash-controller/agent-releases
install -d -m 0755 /usr/local/var/log
for architecture in amd64 arm64; do
  install -m 0700 "/usr/local/minerdash/agent-releases/minerdash-agent-$architecture" \
    "/usr/local/var/minerdash-controller/agent-releases/minerdash-agent-$architecture"
done
launchctl bootout system/com.minerdash.controller 2>/dev/null || true
launchctl bootstrap system /Library/LaunchDaemons/com.minerdash.controller.plist
echo "Miner Dash controller is listening on https://localhost:8443"
EOF
  chmod 0755 "$scripts/postinstall"
  local package="$output/MinerDash-Controller-${version}.pkg"
  pkgbuild --root "$payload" --scripts "$scripts" \
    --identifier com.minerdash.controller --version "$version" \
    --install-location / "$package" >/dev/null
  checksum "$package"
  echo "built $package"
}

(cd "$root" && go test ./...)

if [[ $target == linux ]]; then
  build_linux_deb amd64 amd64
  build_linux_deb arm64 arm64
  build_linux_tarball
else
  build_macos_pkg
fi

rm -rf "$staging"
