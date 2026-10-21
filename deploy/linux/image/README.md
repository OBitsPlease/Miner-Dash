# Reproducible MinerDash raw USB image

The Windows controller's **Rig OS & USB** page supports both direct USB writing
for beginners and a manual balenaEtcher workflow for advanced users. Direct
writing verifies the downloaded compressed image, the decompressed raw-image
hash, and the finished USB contents. After installing Rig OS to an internal
drive, **Restore USB for normal storage** erases the installer USB and creates
one full-size exFAT partition.

Dashboard-created media receives a provisioning file on its EFI setup
partition. It contains the selected controller LAN URL, pinned TLS fingerprint,
rig name, and a one-use enrollment credential. First boot locates the EFI
partition on the boot disk, consumes the pairing file, enrolls automatically,
and removes the file only after enrollment succeeds. A failed enrollment is
retried on a later boot while the credential remains valid.

For headless troubleshooting, the image enables SSH password login for the
`user` account with initial password `1`; the same account is available on the
remote terminal. The local tty1 login prompt is disabled so boot does not stop
at an interactive console. Connect with `ssh user@RIG_IP` after the rig appears
on the network. The account can run administrative diagnostics with `sudo`
using the initial password. The image generates unique SSH host keys on first
boot. Change the initial password with `passwd` after connecting; root SSH
login is disabled.

The builder creates a persistent, bootable raw disk from an operator-supplied
official Ubuntu 24.04 cloud qcow2. It downloads only the explicitly pinned
XMRig package through the image's signature-verifying Ubuntu APT configuration;
it never downloads an agent or proprietary miner. The operator also stages the
Ubuntu `linux-modules-extra` package matching the base kernel, including wired
NIC drivers omitted from the cloud image, and `wireless-regdb`. Ethernet is
required for `network-online.target`, so automatic enrollment waits for the
wired DHCP connection. Verify the Ubuntu digest against the release's signed
`SHA256SUMS` before supplying it.

Install the build-host packages from `packages.txt` using Ubuntu's signed APT
repositories. Build with an independently verified MinerDash agent:

```sh
sudo ./build-raw-usb-image.sh \
  --base ./ubuntu-24.04-server-cloudimg-amd64.img \
  --base-sha256 BASE_SHA256 \
  --agent ./minerdash-agent-amd64 \
  --agent-sha256 AGENT_SHA256 \
  --xmrig-version EXACT_NOBLE_APT_VERSION \
  --xmrig-debs-dir ./xmrig-debs \
  --xmrig-debs-manifest ./xmrig-debs/SHA256SUMS \
  --network-debs-dir ./network-debs \
  --network-debs-manifest ./network-debs/SHA256SUMS \
  --output ./minerdash-usb.raw \
  --size-gib 16
```

The network bundle must contain exactly the `linux-modules-extra` package
matching the base image's installed kernel release and `wireless-regdb`.
Download those exact package versions through signed Ubuntu APT metadata, then
record their SHA-256 digests in the network package manifest:

```sh
apt-get download \
  linux-modules-extra-KERNEL_RELEASE=EXACT_KERNEL_PACKAGE_VERSION \
  wireless-regdb=EXACT_WIRELESS_REGDB_VERSION
cd ./network-debs && sha256sum *.deb > SHA256SUMS
```

The image builder verifies the package names, manifest hashes, kernel release,
and presence of the Realtek `r8169` module before accepting the image.

For a GPU rig, stage the signed Ubuntu NVIDIA driver dependency closure for
the target image's installed package state. Include the selected driver
metapackage, `ubuntu-drivers-common`, `nvidia-settings`, `nvidia-xconfig`,
`xserver-xorg-core`, and all packages required to install them. The builder
verifies every package hash, installs the bundle without network access, and
requires the driver tools and kernel module to be present before it emits the
image. NVIDIA images start a headless Xorg server on local-only display `:0`
so the agent can apply per-GPU fan and clock settings without a monitor or
desktop. CPU-only images do not install or start Xorg.

On a matching Ubuntu 24.04 build host, extract the target base image's package
status and download the chosen stable driver with its complete dependency
closure from signed APT metadata:

```sh
guestfish --ro -a ubuntu-24.04-server-cloudimg-amd64.img -i \
  cat /var/lib/dpkg/status > ./image-dpkg-status
apt-get update
mkdir -p ./nvidia-driver-debs
apt-get -y --download-only --no-install-recommends \
  -o Dir::State::status=./image-dpkg-status \
  -o Dir::Cache::archives=./nvidia-driver-debs \
  install ubuntu-drivers-common=EXACT_UBUNTU_DRIVERS_VERSION \
  nvidia-driver-580=EXACT_NVIDIA_DRIVER_VERSION nvidia-settings \
  nvidia-xconfig xserver-xorg-core
cd ./nvidia-driver-debs && sha256sum *.deb > SHA256SUMS
```

Add these options to the builder invocation:

```sh
--nvidia-driver-package nvidia-driver-580 \
--nvidia-driver-version EXACT_NVIDIA_DRIVER_VERSION \
--nvidia-driver-debs-dir ./nvidia-driver-debs \
--nvidia-driver-debs-manifest ./nvidia-driver-debs/SHA256SUMS
```

The driver series and exact package version are recorded in the build
manifest. The image builder installs from this verified local bundle; no
internet access is required in the Rig OS image or during its first boot.

The selected size must fit the destination USB media. The root filesystem is
expanded to that size and remains writable, so the rig runs persistently rather
than as a live overlay. The builder refreshes both UEFI and legacy BIOS boot
support. Cloud-init and Snap are removed from the finished appliance so cloned
identity is controlled only by MinerDash and startup is not blocked by cloud
metadata or Snap seeding. Write the image only after confirming the output hash:

```sh
sudo dd if=./minerdash-usb.raw of=/dev/SELECTED_USB bs=16M \
  iflag=fullblock oflag=direct conv=fsync status=progress
```

Always resolve `/dev/SELECTED_USB` with `lsblk` immediately beforehand. This
operation destroys that device.

Stage `xmrig`, `libfmt9`, and `libhwloc15` Debian packages from an equivalent
Ubuntu 24.04 host whose signed APT metadata has been updated, then record their
SHA-256 digests in the supplied manifest. For example, use `apt-get download`
with an exact XMRig version selected using `apt-cache policy xmrig`. The
builder accepts only those package names, verifies every digest and the exact
XMRig version, installs them without image-build network access, and verifies
`/usr/bin/xmrig`. This signed GPL package is the only miner installed
automatically.

## Optional miners

Miners are never fetched or selected automatically. Place reviewed,
operator-supplied binaries in a directory and create a manifest in the format
shown by `miners.manifest.example`. Then add:

```sh
--miners-dir ./reviewed-miners --miners-manifest ./miners.manifest
```

Every path and SHA-256 digest is validated before injection. The verified
manifest is retained at `/opt/minerdash/miners/SHA256SUMS.operator` in the
image. Licensing and redistribution remain the operator's responsibility.
The adjacent build manifest records all input hashes, relevant build-tool
versions, selected root partition, image size, and the final raw-image hash.
Proprietary miners continue to use only this operator-supplied manifest flow.

## Identity and enrollment safety

The base image contains no enrollment token, agent identity, machine ID, SSH
host key, wallet secret, or miner configuration. Dashboard-paired USB media
boots directly into Rig OS; the first-boot service creates unique host
identity and enrolls from the EFI pairing file without console input. It keeps
the agent disabled if pairing data is missing or enrollment fails. Never
distribute paired USB media as a master image; create a new paired USB for each
rig.

To clone a running USB installation onto a non-removable local disk, inspect
open that rig's **Commands** tab in Miner Dash and use **Install Rig OS to an
internal drive**. The dashboard lists only unmounted, non-removable disks and
requires explicit erase confirmation. The script refuses the running disk,
removable disks, mounted targets, and undersized targets. It stops the agent
and freezes the running USB root filesystem before copying, transfers the
existing rig identity to the internal disk, and disables that identity on the
source USB. Keep the rig powered during cloning; after it completes, remove the
USB and boot the internal drive. Pair the USB again before reusing it.

Package the completed raw image for dashboard publication:

```sh
./package-rig-os-image.sh ./minerdash-usb.raw ./minerdash-rig-os.img.xz
```

The packager validates the XZ stream and writes the `.sha256` sidecar expected
by the controller.
