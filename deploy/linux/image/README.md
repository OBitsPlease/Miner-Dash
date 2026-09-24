# Reproducible MinerDash raw USB image

The Windows controller's **Rig OS & USB** page supports both direct USB writing
for beginners and a manual balenaEtcher workflow for advanced users. Direct
writing verifies the downloaded compressed image, the decompressed raw-image
hash, and the finished USB contents. After installing Rig OS to an internal
drive, **Restore USB for normal storage** erases the installer USB and creates
one full-size exFAT partition.

Dashboard-created media also receives a provisioning file on its EFI setup
partition. It contains the selected controller LAN URL, pinned TLS fingerprint,
rig name, and a one-use enrollment credential. First boot consumes the file,
enrolls automatically, and removes it. A failed enrollment is retried on a
later boot while the credential remains valid.

The builder creates a persistent, bootable raw disk from an operator-supplied
official Ubuntu 24.04 cloud qcow2. It downloads only the explicitly pinned
XMRig package through the image's signature-verifying Ubuntu APT configuration;
it never downloads an agent or proprietary miner. Verify the Ubuntu digest
against the release's signed `SHA256SUMS` before supplying it.

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
  --output ./minerdash-usb.raw \
  --size-gib 16
```

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

The image contains no enrollment token, agent identity, machine ID, SSH host
key, wallet secret, or miner configuration. On first boot,
`minerdash-first-boot.service` creates unique host identity, keeps the agent
disabled, and requires explicit per-rig enrollment. Never enroll a master
image. The physical console signs in to the locked, non-SSH `minerdash` setup
account and opens a guided menu for USB operation, local-drive installation,
network information, enrollment, reboot, and shutdown. Enroll without placing
the shared token in shell history:

```sh
sudo minerdash-enroll-console
```

The account has passwordless sudo access only to the console enrollment and
local-disk installation commands.

To clone a running USB installation onto a non-removable local disk, inspect
`lsblk`, then use the deliberately destructive command:

```sh
sudo minerdash-install-to-disk \
  --target /dev/SELECTED_DISK \
  --confirm ERASE-/dev/SELECTED_DISK
```

The script refuses the running disk, removable disks, mounted targets, and
undersized targets. Before copying, it stops the agent and freezes the running
USB root filesystem; this prevents an inconsistent live block-level clone.
It then copies the whole bootable disk and removes cloned MinerDash
credentials, machine identity, and SSH host keys from the target. Shut down
without returning to normal operation, remove the USB, and boot the local disk
to create fresh identity and enroll it independently.

Package the completed raw image for dashboard publication:

```sh
./package-rig-os-image.sh ./minerdash-usb.raw ./minerdash-rig-os.img.xz
```

The packager validates the XZ stream and writes the `.sha256` sidecar expected
by the controller.
