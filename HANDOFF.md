# Miner Dash Complete Handoff

Date: 2026-10-21
Project: `C:\Exit20482\MinerDash`
Public repository: `https://github.com/OBitsPlease/Miner-Dash`
Current public version: `1.0.4`
Latest pushed commits: `2a96005`, `5310f27`, `5fcf511`, `63ab3eb`, `fa45f02`
Backup source: `C:\Exit20482\MinerDash-backup-20261017-001229`
Live controller host: `192.168.1.79`
Live GPU rig: `192.168.1.248`
Rig name: `6X3060ti1X4060TI`

## Credential and safety policy

This file intentionally excludes passwords, bearer tokens, private keys, wallet passwords, wallet seeds, and other secrets.

Do not commit credentials or wallet secrets to the repository.

The GPU power policy must remain enforced at 135 W per GPU unless the owner explicitly changes it.

## Application overview

Miner Dash is a local-first Go controller and Linux agent platform for managing mining rigs.

### Controller

- Go HTTP server in `cmd/minerdash-server`.
- Persistent state stored in a JSON-backed store.
- Live state directory: `C:\Users\tourj\AppData\Local\MinerDash`.
- HTTPS dashboard normally listens on port 8443.
- Embedded web UI lives in `internal/server/web`.
- Resources include farms, wallets, pools, miners, flight sheets, overclock profiles, schedules, alerts, and users.
- Controller queues rig commands and receives agent heartbeats and command results.
- Controller-wide GPU power policy is stored with the controller state.

### Linux agent

- Go agent in `cmd/minerdash-agent` and `internal/agent`.
- Installed on the rig at `/opt/minerdash/agent/current/minerdash-agent`.
- Service: `minerdash-agent.service`.
- Agent reads `/etc/minerdash/agent.json`.
- Agent reports GPU telemetry, miner state, hashrate, shares, temperatures, power, fan speeds, and configuration errors.
- Managed miner binaries are stored under `/opt/minerdash/miners`.
- The agent launches miner processes directly without a shell.
- Start and Restart now resolve the assigned flight sheet before launching.

### Browser UI

- Main UI source: `internal/server/web/index.html`.
- Application logic: `internal/server/web/app.js`.
- Styling: `internal/server/web/style.css`.
- Wallet, pool, miner, flight-sheet, worker, GPU tuning, diagnostics, and command-console screens are rendered client-side.
- `loadAll()` refreshes resources from the controller API.
- Worker cards refresh live telemetry from `/api/v1/rigs`.

### Important API routes

- `GET /api/v1/rigs`
- `GET /api/v1/wallets`
- `POST /api/v1/wallets`
- `PUT /api/v1/wallets/{id}`
- `GET /api/v1/pools`
- `GET /api/v1/miners`
- `GET /api/v1/flight-sheets`
- `PUT /api/v1/flight-sheets/{id}`
- `POST /api/v1/rigs/{id}/commands`
- `GET /api/v1/rigs/{id}/commands`
- `GET /api/v1/agents/{id}/configuration`

## Completed Miner Dash source changes

### Troubleshooting UI

Files:

- `internal/server/web/index.html`
- `internal/server/web/app.js`
- `internal/server/web/style.css`

Implemented:

- Added Start miner button.
- Existing Stop miner and Restart miner controls remain available.
- Added GPU PCI and driver diagnostics.
- Added GPU kernel error diagnostics.
- Added network and DNS diagnostics.
- Added failed-service diagnostics.
- Added assigned-flight-sheet and assigned-software hint when the miner is stopped.
- Existing diagnostics include NVIDIA GPU inventory, system version, network, disks, disk space, memory, agent status, and agent logs.

### Flight-sheet-aware Start and Restart

File:

- `internal/agent/client.go`

The old command path reused only the last miner profile. It could start stale arguments after a flight-sheet change.

The corrected path:

1. Synchronizes the current assigned configuration.
2. Resolves the current wallet, pool, worker, coin, algorithm, and arguments.
3. Prepares managed miner packages.
4. Applies GPU power and tuning policy.
5. Starts the resolved flight-sheet workload.
6. Reports startup errors back through command results.

### Stratum protocol validation

File:

- `internal/server/resources.go`

Added support for these endpoint schemes:

- `stratum1+tcp`
- `stratum1+ssl`
- `stratum1+tls`
- `stratum1+tcps`

This is needed for miners that use an Eth-proxy-compatible Stratum variant.

### Build and test validation

Passed after the source changes:

- `go test ./...`
- `go build ./...`
- `node --check internal/server/web/app.js`
- `git diff --check`

## Live controller state and restoration

The original live state was found at:

`C:\Users\tourj\AppData\Local\MinerDash`

It contains:

- `state.json`
- `secrets.json`
- TLS certificate and key
- Managed miner packages
- Agent releases
- Telemetry history
- Previous state backups

The backup source directory:

`C:\Exit20482\MinerDash-backup-20261017-001229`

contains source/build artifacts and controller logs, but not the live state database.

The controller was accidentally restarted once against a new empty data directory, generating a new certificate and causing the rig to reject TLS. The original controller was restored using the live state directory above.

Never start the production controller with a new empty `-data` directory.

## Public release status

The public 1.0.6 source, tag, and release are published on GitHub. Release `v1.0.6` is the current public latest release.

Windows installer direct download:

`https://github.com/OBitsPlease/Miner-Dash/releases/download/v1.0.6/MinerDash-Setup-1.0.6.exe`

The 1.0.6 release contains the WildRig GPU telemetry parser and the compact all-miner overview redesign.

WildRig telemetry now parses the miner's structured statistics output for:

- Per-GPU hashrate and GPU index
- Overall hashrate
- Accepted shares
- Per-GPU hardware values already collected by Miner Dash

The overview now keeps per-GPU data visible on collapsed rig cards in a compact Hive-style row layout. It applies to every miner because the renderer consumes the common `MinerState` and `GPU` telemetry models rather than a WildRig-specific UI. Each GPU row can show hashrate, temperature, fan, power, utilization, core and memory clocks, power limit, and configured core/memory offsets. Focused rig view still exposes driver, firmware, PCI, memory, and detailed controls.

Dual CPU/GPU flight sheets now show the secondary workload beside the primary workload in the overview when configured, including the second coin and algorithm. The same layout supports SRBMiner and other miners that report multiple device workloads.

Validation passed:

- `go test ./...`
- `go build ./...`
- `node --check internal/server/web/app.js`
- `git diff --check`
- GitHub Actions Windows installer workflow
- GitHub Actions macOS/Linux controller installer workflow
- Direct installer URL returned HTTP 200

The local controller was rebuilt and restarted at version 1.0.6 using the existing production data directory. The local health endpoint reported `{"status":"ok","version":"1.0.6"}`.

The Windows installer and companion packages are published in release `v1.0.6`. The USB/Rig OS payload remains inherited from the verified release input used by the packaging workflow; the controller installer and release assets are updated to 1.0.6.

Public release exclusions include the handoff, controller state, credentials, TLS keys, telemetry, logs, package caches, generated binaries, Debian artifacts, and local runtime data.

README policy: keep public documentation focused on user-facing installation and operation. Do not reintroduce unnecessary implementation details or competitor references unless directly needed by users.


Host: `192.168.1.248`

Hardware:

- GPU 0: NVIDIA GeForce RTX 4060 Ti
- GPUs 1-6: NVIDIA GeForce RTX 3060 Ti
- NVIDIA driver: `580.178.04`
- Ubuntu 24.04-based environment
- Miner Dash agent service active

Current agent paths:

- `/opt/minerdash/agent/current/minerdash-agent`
- `/etc/minerdash/agent.json`
- `/opt/minerdash/miners`

## GPU power policy

The controller-wide GPU policy is set to:

- 135 W per GPU

The physical limits were also applied with `nvidia-smi`.

The current rig telemetry has shown all GPUs at 135 W, except GPU 2 may use a profile-specific 132 W limit from its assigned overclock profile.

Keep this policy active. Do not raise it while diagnosing mining unless explicitly requested.

## Permanent fan-control implementation

The live fan-control fix includes both the rig display service and the agent mapping correction.

Rig files:

- `/etc/minerdash/xorg.conf`
- `/etc/systemd/system/minerdash-xorg.service`

Repository source files:

- `internal/agent/autofan.go`
- `internal/agent/autofan_test.go`

Changes:

- Added a headless Xorg display on `:0` with NVIDIA Coolbits enabled.
- Enabled the Xorg service at boot before the Miner Dash agent.
- Changed NVIDIA fan mapping to apply discovered fan targets across the shared NVIDIA controller topology instead of assuming positional GPU/fan groups.
- Updated the unit test for shared fan-controller behavior.
- Rebuilt and deployed the Linux AMD64 agent to `/opt/minerdash/agent/current/minerdash-agent`.

Verified live:

- All seven GPU fan values report 50%.
- All seven GPUs remain at 135 W power limits.
- Miner process remains running.
- GPU temperatures and fan telemetry are updating.

### Fan-control implementation history

The rig had `nvidia-settings` installed, but no active Xorg control display. It returned:

```text
The control display is undefined
```

The existing `minerdash-nvidia-xorg.service` only enabled NVIDIA persistence mode. It did not start Xorg.

### Rig files added

- `/etc/minerdash/xorg.conf`
- `/etc/systemd/system/minerdash-xorg.service`

The service starts headless Xorg on display `:0` with NVIDIA `Coolbits` enabled and is enabled at boot.

### Verified result

- `nvidia-settings -q '[gpu:0]/GPUFanControlState'` works.
- `nvidia-settings -q '[gpu:3]/GPUFanControlState'` works.
- Manual fan targets can be set.
- All seven GPUs were set to 50% fan through the Miner Dash overclock profile.
- Fan telemetry reported 50% on all seven GPUs after the fix.
- GPU power limits remained enforced.

A harmless `libXv.so.1` warning remains in `nvidia-settings` output. It does not prevent fan control.

## VEIL work completed

The original VEIL flight sheet used SRBMiner-MULTI with `progpow-veil`. That package did not provide a usable VEIL GPU path on this rig.

The correct catalog miner was installed:

- Catalog ID: `veil-miner-gpu`
- Miner: Veil GPU Miner
- Algorithm: `progpow-veil`

Missing runtime packages installed on the rig:

- `libboost-thread1.83.0`
- `libboost-filesystem1.83.0`
- `libnvrtc12`
- `libnvrtc-builtins12.0`

The VEIL daemon was confirmed synced and returned valid ProgPow templates through RPC.

The custom VEIL proxy was proven to:

- Accept the miner connection.
- Deliver ProgPow jobs.
- Receive submitted shares.
- Relay accepted shares to the daemon.

The VEIL proxy logs showed accepted submissions.

## VEIL proxy and dashboard

VEIL project directory:

`C:\Users\tourj\VEIL SOLO STRATUM`

Relevant files:

- `veilproxy.py`
- `server.js`
- `dashboard.html`
- `config.json`
- `runtime.json`
- `proxy-stats.json`
- `veilproxy-live.err.log`
- `server-live.err.log`

The VEIL dashboard originally showed zero miners because it tracked the Node.js Stratum session registry while the active miner used the Python proxy.

Work attempted:

- Added Python proxy session logging.
- Added proxy snapshot generation through `proxy-stats.json`.
- Updated Node dashboard logic to read proxy statistics.
- Added dashboard-only mode so Node does not compete with the Python proxy for the Stratum port.

Known status:

- The Python proxy successfully shows the miner connection and accepted shares in its log.
- The dashboard integration has been partially corrected but requires a final clean runtime verification after proxy restarts.
- The reverse SSH tunnel may be required when Windows inbound filtering blocks direct rig-to-controller access.

Reverse tunnel script:

`C:\Exit20482\MinerDash\build\reverse_tunnel.py`

It maps the rig's local `127.0.0.1:3335` to the controller's local `127.0.0.1:3335`.

## Current PEARL / PRL state

Current flight sheet:

- Name: `PEARL`
- Coin: `PRL`
- Algorithm: `pearlhash`
- Miner: WildRig Multi `0.51.5`
- Worker: `BITS`
- Current wallet address is preserved.
- Hive-compatible wallet template:

```text
{WALLET}@nock=5Zw3csdcwowhxHdTaAPT5GbkuNti3smoQuhWac6EvTsm8CjbWv8B2dS
```

The original PEARL issue was a stale pool override and an incomplete Hive-compatible wallet login. The active WildRig command must be checked against the flight sheet before diagnosing mining.

The Hive-tested endpoint was:

- `stratum+tcp://pool.pearlhash.xyz:9000`
- Password `x`
- Algorithm `pearlhash`

Direct testing showed that endpoint accepts TCP but did not return a standard Stratum subscribe response. The known responsive diagnostic endpoint was:

- `stratum+tcp://ca.pearl.herominers.com:1200`

The final chosen pool must be one that returns Stratum jobs for WildRig. Do not treat a TCP-open result as proof that the pool is usable.

### PEARL current verified state

The live PEARL flight sheet uses WildRig Multi `0.51.5` with the Hive-compatible wallet template and the pool configuration last tested by the owner. The rig's WildRig process is running with the assigned `pearlhash` algorithm. Recheck pool jobs and shares before calling a session fully productive.


Fan control required the headless Xorg service and the shared NVIDIA fan mapping fix. The current profile target is:

- Fan: 50%
- Power: 135 W

The agent currently reports all seven GPUs at 50% fan after the latest deployment. Verify the assigned profile after any flight-sheet or overclock update.

## Pending todos

### P0: Confirm PEARL pool compatibility

- Confirm the active pool endpoint with a direct Stratum subscribe test.
- Confirm WildRig receives jobs from that endpoint.
- Confirm accepted shares and nonzero hashrate.
- If the chosen pool does not answer Stratum, use the known responsive endpoint or the pool’s documented port/protocol.
- Do not treat TCP-open as proof that a mining pool is usable.

### P0: Make Miner Dash expose pool handshake failures

Add explicit miner health fields for:

- DNS resolution failure
- TCP connection failure
- Stratum subscribe timeout
- authorization failure
- no-job timeout
- zero hashrate while process is running
- accepted/rejected share counters

Show these in the worker card and Miner screen instead of only showing `running: true`.

### P0: Fix command-result semantics for early miner failures

The agent currently reports a Start command as successful when the child process launches, even if it exits or remains suspended shortly afterward.

Add a short post-launch health window and return a failed command result when:

- the process exits immediately
- the miner has no pool connection after a timeout
- the miner reports zero hashrate after initialization

### P1: Finish VEIL dashboard proxy integration

- Ensure `proxy-stats.json` is updated on every connection and share event.
- Ensure Node dashboard-only mode reads it reliably.
- Display Python proxy accepted/rejected shares and active miners.
- Avoid two competing Stratum servers on the same port.
- Add a single service/process supervisor for the proxy and dashboard.

### P1: Fix NVIDIA fan mapping permanently

The current fan mapper assumes the number of NVIDIA fans divides evenly across GPU indices. The hardware exposed 14 fans across seven GPUs and worked after Xorg was added, but the mapping should be validated against actual NVIDIA target ownership rather than position alone.

Add tests for:

- 14 fans across seven GPUs
- one fan per GPU
- uneven fan counts
- missing GPU fan targets
- Xorg unavailable

### P1: Add runtime dependency validation for managed miners

Before starting a managed binary, run a safe dependency check and report missing libraries clearly. For example:

- `libboost_thread.so`
- `libboost_filesystem.so`
- `libnvrtc.so`
- CUDA runtime libraries

Do not wait for a generic process exit status 127.

### P1: Add pool configuration protection

When a flight sheet has a pool override or backup pool, show it prominently beside the primary pool. Require explicit confirmation before using an override. Display the exact resolved pool command in the Miner screen.

### P2: Wallet creation regression coverage

The live API wallet path was tested successfully:

- POST wallet created a wallet.
- GET wallets returned it.
- DELETE removed the test wallet.

Add browser-level coverage for:

- Add wallet form submission.
- Refresh after save.
- Error display when validation fails.
- Coin filter showing the newly created wallet.

### P2: Improve deployment workflow

- Always build Linux agents with `GOOS=linux GOARCH=amd64` before rig deployment.
- Never deploy the Windows agent binary to Linux.
- Keep controller data in `C:\Users\tourj\AppData\Local\MinerDash`.
- Add a deployment command that detects architecture and verifies ELF format before installation.
- Add an explicit backup and rollback step before agent replacement.

### P2: Consolidate documentation and runtime configuration

- Keep the controller data path in one configuration source.
- Add a supported service definition for the controller.
- Add a supported service definition for the VEIL proxy/dashboard.
- Remove stale development processes and duplicate proxy instances.
- Add a runtime status screen showing controller path, agent version, proxy endpoint, pool endpoint, and last successful share.

## Current verification commands

Controller tests/build:

```powershell
go test ./...
go build ./...
node --check internal\server\web\app.js
git diff --check
```

Rig service and GPU status:

```bash
systemctl status minerdash-agent --no-pager
journalctl --namespace=minerdash -n 120 --no-pager
nvidia-smi
```

WildRig process:

```bash
ps -eo pid,ppid,args | grep -E '[w]ildrig-multi'
```

Pool TCP test:

```bash
timeout 8 bash -c '</dev/tcp/POOL_HOST/POOL_PORT'
```

Never assume a successful TCP test means the pool is returning mining jobs.
