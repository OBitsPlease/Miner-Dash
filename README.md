# MinerDash

MinerDash is a local-first CPU and GPU mining management platform. A controller
runs as a Windows service and serves a browser dashboard. Linux workers run a
small agent that monitors hardware, applies assigned mining configurations, and
executes a restricted command set.

MinerDash has no billing, hosted control plane, pool preference, developer
wallet, or built-in hashrate redirection. Wallet entries are public payout
addresses only. Never put wallet seed phrases or private keys on a mining rig.

## Install on Windows

No repository clone or command line is required.

1. Download **[MinerDash-Setup-1.0.2.exe](https://github.com/OBitsPlease/Miner-Dash/releases/download/v1.0.2/MinerDash-Setup-1.0.2.exe)**.
2. Double-click the installer and approve the Windows administrator prompt.
3. Save the first-login credentials displayed during setup.
4. Open the **Mining Dash** desktop shortcut.

Miner Dash is currently a beta and the installer is not yet code-signed.
Windows SmartScreen may require **More info → Run anyway**. Release downloads
also include SHA-256 checksums so the installer can be verified before use.

## Platform features

- Farms, workers, tags, and bulk miner actions
- Compact farm operations view with tabbed full-rig detail screens
- Separate GPU Mining and CPU Mining workspaces
- Dedicated wallet workspace with coin logos and current USD prices
- One guided setup form for coin, wallet, pool, miner, algorithm, and rigs
- Individually named worker cards with per-rig statistics and settings
- Controller-observed LAN IP address on every worker card
- Per-rig wall-power calibration with fixed CPU estimates and GPU system-overhead correction
- Per-worker shortcuts to its MinerDash-managed miner output and assigned pool worker page
- Cached CoinGecko prices and optional pool API totals for blocks, payouts, mined coins, and USD value
- Farm overview totals for online rigs, GPU count, active CPU mining threads, power, coins,
  and hashrates grouped by workload
- SRBMiner-MULTI CPU + GPU dual-coin flight sheets
- HTTPS Custom Miner imports with server-side SHA-256 pinning
- Hourly cached official GitHub release checks and one-click verified updates for supported catalog miners
- Per-rig orange/gray miner-update indicators with official release notes and assigned-rig rollout
- Transactional miner updates that preserve flight sheets and automatically restore the previous binary if startup fails
- Six-hour GitHub application-update checks with a top-right orange indicator and checksum-verified one-click installation
- Compact Discord community link for questions, issue reports, and feature suggestions
- Per-GPU power, fan, core, and memory tuning from each rig card
- Reusable wallets and approved Stratum pool endpoints
- Compact, coin-filtered flight sheets with overview, basic setup, and detailed miner configuration views
- Per-flight-sheet wallet templates, worker names, pool overrides, backup endpoints, passwords, and extra arguments
- Local or controller-managed miner executables
- SHA-256 verification before a managed miner is executed
- Reviewed source URL and commit metadata for miner packages
- NVIDIA and AMD GPU inventory and telemetry
- Per-GPU driver, VBIOS/firmware, and PCI identity reporting
- Stable Ubuntu-family NVIDIA and AMD driver updates without third-party OS scripts or repositories
- Private local driver-efficiency learning by GPU model, coin, algorithm, and driver, with a 60-minute evidence threshold and update guard
- Gear-based per-GPU overclock editor with one-click import of locally measured knowledge presets
- Per-GPU 24-hour activity, temperature, fan, power, algorithm hashrate, and total-power charts
- CPU, memory, uptime, OS, kernel, and miner telemetry
- XMRig-compatible hashrate and share telemetry
- NVIDIA power limits
- AMD power limits and fixed fan controls
- Temperature-based automatic fan controls
- Local temperature and hashrate watchdogs
- Temperature, hashrate, miner, and offline-worker alerts
- One-minute telemetry history with at least 32-day retention, date/range navigation, and SVG chart downloads
- Local schedules for flight sheets and worker actions
- Miner log retrieval
- Reboot, shutdown, start, stop, and restart commands
- Administrator, operator, and read-only viewer accounts
- Activity audit log
- TLS with per-installation certificate generation
- Per-agent credentials and controller certificate pinning
- Configuration backups and release hashes
- Persistent Rig OS images that boot and mine directly from USB
- Optional USB-to-local-drive installation
- Built-in miner catalog with controller-managed package updates

## Security model

- Miner commands are executed directly. They are never passed through a shell.
- Preinstalled miner executable paths must be absolute.
- Managed miners are downloaded only from the pinned local controller and must
  match their recorded SHA-256 digest.
- Agents may download only the miner currently assigned to them.
- The controller accepts a fixed command allowlist.
- Passwords use PBKDF2-HMAC-SHA256 with unique salts and 210,000 iterations.
- Administrator and agent bearer tokens are stored as SHA-256 hashes.
- Browser sessions use bearer tokens in session storage, not cookies.
- Agents pin the exact controller certificate.
- TLS 1.3 is required.
- Configuration changes and command results are retained in the activity log.

Open source does not make an arbitrary miner binary safe. For strict fee
control, build the miner from a reviewed source commit, upload that exact
binary, record its source commit, and restrict worker network access to an
approved Stratum gateway.

## Build a release

Go 1.26 or later is required.

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\scripts\build-release.ps1
```

This tests the code and creates:

- `build\minerdash-server.exe`
- `build\minerdash-agent-amd64`
- `build\minerdash-agent-arm64`
- `build\miningdash-windows-installer.zip`
- `build\minerdash-rig-os.img.xz`
- `build\minerdash-rig-os.raw.build-manifest`
- `build\SHA256SUMS.json`

The release build requires the verified Rig OS artifacts under
`build\rig-os`. It packages them into the Windows installer for an offline,
ready-to-write first run and also emits them as standalone release assets.

## Run the controller interactively

```powershell
.\build\minerdash-server.exe -listen :8443 -data .\data
```

Open `https://localhost:8443`. The first run creates and prints:

- A bootstrap administrator token
- A worker enrollment token
- A self-signed TLS certificate
- The certificate's SHA-256 fingerprint

The browser will show a warning until the local certificate is trusted. Review
the fingerprint, then use:

```powershell
.\scripts\trust-controller-certificate.ps1 -CertificatePath .\data\tls.crt
```

The bootstrap token can create named local users under **Users**. Use a named
account for routine access and protect the bootstrap token as a recovery
credential.

## Multiple rigs and mining setups

Every OS installation enrolls as a separate worker. Give each rig a unique,
recognizable name such as `CPU-Rig-01`, `CPU-Rig-02`, or `GPU-Rig-AMD-01`.
The Workers page groups reporting machines into GPU rigs and CPU-only rigs.
Each worker retains its own telemetry, flight sheet, overclock profile,
watchdog, autofan policy, logs, and start/stop/reboot controls.

Use **GPU Mining** or **CPU Mining** for normal setup. The guided form creates
or reuses the wallet, pool, and miner records automatically and can apply the
new flight sheet to any number of selected rigs. The separate resource pages
remain under **Advanced configuration** for operators who need direct editing.
Saved wallets and pools appear as choices but can also be entered inline.

Choosing **Custom Miner** in the guided flight-sheet form provides the compact
name, HTTPS installation URL, executable, algorithm, wallet/worker template,
pool password, and argument fields needed for a one-off setup. The Mining
Software Catalog also provides **Build custom miner** for creating a reusable
definition from either a public HTTPS executable or `.tar.gz` package, or an
uploaded Linux package. Self-contained mode extracts only the named executable.
Complete bundle mode safely preserves regular files and directories, launches
the selected archive entry point with optional environment variables, and can
read a supported loopback statistics API. Miner Dash pins both archive and
entry-point SHA-256 values, rejects links and unsafe paths, and never executes
third-party installer or callback scripts. Bundle updates atomically replace
the installation directory and restore the previous directory when startup
fails.
Arguments support `{POOL}`, `{POOL_ENDPOINT}`, `{WALLET}`, `{WORKER}`,
`{PASSWORD}`, `{COIN}`, and `{ALGORITHM}`. Wallet templates additionally
accept legacy `%WAL%` and `%WORKER_NAME%` placeholders for migration. This is
a Miner Dash-native workflow with no external rig-management dependency.
The dedicated **Custom Miner Lab** page provides the reusable-miner builder,
package requirements, placeholder reference, readiness state, and links back
to package management for every custom definition.

Every controller also refreshes the approved community catalog from
`https://github.com/OBitsPlease/MinerDash-Custom-Miners`. Published entries
appear under **Miner Dash community miners** in the Custom Miner Lab and in
Flight Sheet miner selectors without requiring a Miner Dash software update.
Each registry entry pins the extracted executable SHA-256. Bundle entries also
pin the complete archive SHA-256 and define their entry point. Installation is
rejected and cleaned up if either downloaded hash does not match.

The Miner screen shortcut retrieves output directly from the process launched
by the MinerDash agent, without depending on another management agent.
Its output panel is independent of the rig-management form, strips terminal
color escape sequences, and remains open across automatic dashboard refreshes.
Select a rig heading or its **Full view** button to expand that rig across the
browser viewport; press Escape or **Close full view** to return to the compact
farm view.
Restart and stop controls are attached to each individual rig card on Overview,
CPU Mining, and GPU Mining. MinerDash deliberately does not show farm-wide
restart/stop controls; stopping a rig requires confirmation.
Pools can store an optional HTTP or HTTPS worker-page URL template with
`{WALLET}`, `{WORKER}`, and `{COIN}` placeholders. Worker cards expand those
placeholders from the assigned flight sheet and open the exact pool worker page
in a new browser tab. A pool link remains disabled when its URL is not
configured rather than guessing an unsafe or incorrect destination.

The dashboard uses responsive single-column layouts for narrow phone screens,
an icon navigation rail on tablets, wrapping controls, scrollable tables, and a
borderless full-screen rig view on phones.
Choose **Custom Miner** to provide a public HTTPS link to a direct Linux
executable or `.tar.gz` package, its executable name, and command arguments.
MinerDash never executes an installation script: it extracts only the named
binary, hashes it, and serves that pinned file through the authenticated agent
package endpoint.

When SRBMiner-MULTI is selected, **Add CPU + GPU dual workload** adds a second
coin, wallet, pool, algorithm, and device type to the same flight sheet.
MinerDash generates SRBMiner's device-specific `--algorithm-cpu` /
`--algorithm-gpu`, pool, wallet, and password arguments so one process can mine
the two configured workloads on the appropriate devices.

## Install the Windows service

Run PowerShell as Administrator:

```powershell
.\scripts\install-controller.ps1
```

The interactive installer displays the approved wordmark splash artwork with
**Created by BitsPleaseYT**. The wordmark-free emblem is used for the dashboard,
favicon, and desktop shortcut. The installer creates an all-users **Mining
Dash** desktop shortcut that opens the local dashboard. Use
`-NoSplash` for unattended installations. The self-contained
`build\miningdash-windows-installer.zip` includes the controller, installer,
uninstaller, certificate helper, splash, and icon.

The service starts automatically with Windows and restarts after failures.
Program files are installed under `C:\Program Files\MinerDash`; persistent
state, packages, credentials, and certificates are stored under
`C:\ProgramData\MinerDash`. Access to the data directory is restricted to
Administrators and Local System.

Create a consistent backup with:

```powershell
.\scripts\backup-controller.ps1
```

## Configure a Linux worker

The automated Ubuntu 24.04 provisioning workflow is under
[`deploy/linux`](deploy/linux). Copy the repository release files to the rig,
then run:

```bash
sudo scripts/install-linux.sh
sudo scripts/install-agent-release.sh stage ./minerdash-agent-amd64 SHA256
sudo scripts/install-agent-release.sh activate SHA256
sudo scripts/enroll-linux-agent.sh \
  --controller https://CONTROLLER_IP:8443 \
  --fingerprint CONTROLLER_CERTIFICATE_SHA256 \
  --name RIG_NAME \
  --token-file /root/minerdash-enrollment-token
```

Create the token file with mode `0600`; the enrollment script removes it after
successful enrollment. See
[`deploy/linux/OPERATIONS.md`](deploy/linux/OPERATIONS.md) for GPU driver
installation, firewall policy, atomic upgrades and rollback, and reproducible
Ubuntu qcow2 image preparation.

For manual installation:

1. Copy the appropriate Linux agent to
   `/usr/local/bin/minerdash-agent`.
2. Copy [examples/agent.json](examples/agent.json) to
   `/etc/minerdash/agent.json`.
3. Open **Rig OS & USB** in the dashboard and reveal the manual enrollment
   credentials under the existing Linux installation option.
4. Set the controller URL, enrollment token, certificate fingerprint, and rig
   name.
5. Add local miner profiles only when the miner is not distributed as a
   managed package.
6. Install [deploy/minerdash-agent.service](deploy/minerdash-agent.service) in
   `/etc/systemd/system/`.
7. Enable the agent:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now minerdash-agent
```

For an existing systemd-based x86-64 Linux rig, first copy the project's
`deploy/` and `scripts/` directories plus
`build/minerdash-agent-amd64` to the rig. Run
`scripts/inspect-existing-rig.sh` before changing anything. If its root
filesystem is a normal partition, `scripts/expand-root-filesystem.sh --plan`
reports whether unused USB capacity can be reclaimed without modifying the
disk. Use `scripts/install-existing-linux.sh` to stage the verified agent; it does
not stop existing mining services, enroll the rig, or start MinerDash. Perform
those steps only after reviewing the inspection output so two control agents
never operate the same miner simultaneously.

After enrollment, remove `enrollment_token` from the configuration. The
worker's unique credential is stored with mode `0600`.

## Build and publish the Rig OS USB image

The **Rig OS & USB** dashboard section offers two setup paths. Beginner mode lists only removable Windows USB disks, uses the verified image
included with the controller (or downloads a newer published image), writes it
directly, verifies the completed USB, and adds one-use automatic enrollment.
Advanced mode
provides the compressed image, checksum manifest, and balenaEtcher link.
Direct writing excludes Windows boot and system disks, rechecks the selected
device immediately before erasing, and requires an exact confirmation phrase.
The dashboard also writes a controller-specific provisioning file containing
the selected LAN address, pinned TLS certificate fingerprint, requested rig
name, and a one-use enrollment credential. On first boot the rig enrolls
automatically, removes that provisioning file, and starts reporting to the
controller. Advanced users who flash with balenaEtcher can return to the
dashboard and pair the completed USB without reflashing it.
The image is a persistent operating-system disk: a rig can continue running
from USB or clone the same installation to an explicitly selected local disk.
Flashing does not reduce a USB drive's physical capacity. Windows may display
only the small boot partition because it does not mount Linux filesystems; the
dashboard's **Restore USB for normal storage** action can erase those Linux
partitions and create one full-size exFAT partition after the USB is no longer
needed. On an existing Linux installation,
`scripts/expand-root-filesystem.sh --plan` safely reports whether the root
partition can grow into currently unallocated space. Its `--apply` mode
requires root plus an exact partition-specific confirmation and refuses
overlay, LVM, RAID, read-only, and unsupported filesystems.

Build the image inside Ubuntu 24.04 or WSL using an official Ubuntu 24.04
cloud image, its published SHA-256 digest, and a release agent built by this
project. The builder never downloads a base image, agent, or miner implicitly.
See [`deploy/linux/image/`](deploy/linux/image) for the builder and manifest
format.

Publish a replacement compressed image and its build manifest to a Windows
controller:

```powershell
.\scripts\publish-rig-os.ps1 `
  -ImagePath .\minerdash-rig-os.img.xz `
  -BuildManifestPath .\minerdash-rig-os.raw.build-manifest
```

The controller exposes the image only after its SHA-256 sidecar and raw-image
build manifest are installed.
Downloads use short-lived tickets so large images stream through the browser
without putting administrator credentials in a URL.

The catalog includes Rigel, WildRig Multi, SRBMiner-MULTI, miniZ, XMRig,
lolMiner, TeamRedMiner, BzMiner, GMiner, T-Rex, Ethminer, Hellminer,
OneZeroMiner, QubMiner, CPUMiner-OPT, BCIII Hash Miner, and a permanent Custom
miner option. Rigel, WildRig Multi, SRBMiner-MULTI, miniZ, lolMiner, BzMiner,
and OneZeroMiner support automatic installation from allow-listed official
release repositories; XMRig is included in Rig OS. Selecting one of these in
the guided flight-sheet form downloads and hashes it on the controller, assigns
the sheet, and causes each selected agent to download, verify, and start it.
Entries without a safe current Linux release remain available for monitored or
manual setup and explain the applicable compatibility or package limitation.
Changing that rig to another flight sheet stops the previous miner and performs
the same verified installation and start sequence for the newly selected
software.

Most GPU miners are proprietary. Automatic installation downloads their public
release archive for private use on your rigs; review each vendor's license and
developer-fee policy. Catalog entries without an automatic official-release
installer remain under Advanced configuration and require an operator-supplied
package. XMRig is open source under GPL-3.0 and may be redistributed only while
meeting its license and corresponding-source obligations.

## Miner definitions

A miner definition can reference either:

1. A profile preinstalled in `agent.json`; or
2. A controller-managed Linux executable uploaded through the Miners screen.

Flight-sheet arguments are separate array elements. Supported substitutions
are:

- `{POOL}`
- `{WALLET}`
- `{PASSWORD}`
- `{WORKER}`
- `{COIN}`

Because no shell interprets these values, shell operators embedded in a wallet,
pool, or worker name are not executed.

For XMRig-compatible telemetry, a preinstalled profile can specify:

```json
{
  "stats_type": "xmrig",
  "stats_url": "http://127.0.0.1:18080/2/summary"
}
```

Statistics endpoints are restricted to HTTP loopback addresses.

## Network isolation

Production workers should use a dedicated VLAN. Deny outbound connections by
default and permit only:

- The local MinerDash controller
- Approved Stratum gateways or mining pools
- Internal DNS and NTP
- Temporarily approved operating-system update mirrors

The controller computer can use the internet normally. Do not configure router
port forwarding that exposes controller TCP port `8443`, worker SSH, miner
APIs, or GPU management ports directly to the public internet. Use the
owner-specific Cloudflare Tunnel setup or another authenticated private-access
method for access away from the local network.
