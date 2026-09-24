# MinerDash Linux operations

This layer targets Ubuntu 24.04 LTS. Keep rigs on a dedicated, non-routed
mining VLAN. Permit administrators into that VLAN only through a management
gateway. The generated nftables policy denies forwarding and unsolicited
inbound traffic, and permits only the pinned controller, DNS/NTP, and explicitly
listed pool gateways. Prefer internal Stratum gateways so worker VLANs never
need broad Internet access. Apply firewall policy from a local console first.
After validating access, persist it by adding
`include "/etc/nftables-minerdash.conf"` to `/etc/nftables.conf`, validating
with `nft -c -f /etc/nftables.conf`, and enabling `nftables.service`.

## Supply-chain policy

`install-linux.sh` installs only signed Ubuntu packages. It never retrieves an agent
or miner. Supply an agent release built by the project and its SHA-256 digest
to `install-agent-release.sh`. Miner binaries must be reviewed, built or
obtained separately, uploaded to the local controller, and digest-pinned there.

For NVIDIA, run `setup-gpu-drivers.sh plan`, review the signed Ubuntu APT
candidate and upstream compatibility, then install with the exact package and
version. Never use NVIDIA `.run` installers. AMDGPU is in Ubuntu's kernel and
Mesa stack; the script installs no third-party ROCm repository. Pin and review
a vendor repository separately if a workload truly requires ROCm.

## Enrollment and upgrades

Enrollment consumes a root-only token file, waits for the per-agent identity,
then removes the shared token from both disk and configuration. Do not place
tokens in shell history, image metadata, environment variables, or command
arguments.

Agent releases are immutable digest-named directories. `current` and
`previous` symlinks provide atomic activation and rollback. Stage and validate
an upgrade before activation:

```sh
sudo scripts/install-agent-release.sh stage ./minerdash-agent SHA256
sudo scripts/install-agent-release.sh activate SHA256
sudo scripts/install-agent-release.sh rollback
```

Review `journalctl --namespace=minerdash -u minerdash-agent` after activation.
The dedicated journal namespace is compressed and bounded by its installed
drop-in, without changing retention for other system services.

## VLAN image builds

`image/build-qcow2.sh` uses Ubuntu's `cloud-image-utils` and `qemu-img` tooling
with an official Ubuntu 24.04 cloud image supplied by the operator. It does not
download images. Verify the image against Ubuntu's signed SHA256SUMS first.
Install the build-host dependencies listed in `image/packages.txt` from Ubuntu
APT; they are intentionally excluded from the runtime rig package manifest.
The output is a copy-on-write qcow2 plus a NoCloud seed. Boot them together,
run provisioning, enroll each cloned rig independently, then discard the seed.

For deployable persistent USB media, use `image/build-raw-usb-image.sh` and
follow `image/README.md`. That builder verifies the operator-supplied Ubuntu
qcow2, agent, and optional miner manifest before producing a bootable raw disk.
It installs only an explicitly pinned `xmrig` version from Ubuntu's signed
24.04 APT repositories; proprietary miners remain operator-supplied and
SHA-256-verified.
It embeds no enrollment identity or secret. The installed
`minerdash-install-to-disk` command can explicitly clone a running USB system
to a selected non-removable local disk while forcing new identity on next boot.
