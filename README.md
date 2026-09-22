# Miner Dash

Miner Dash is a self-hosted mining controller and persistent Linux rig operating
system. It is designed to manage rigs without requiring Hive OS.

## Beta testing

The current beta is intended for testers.

### Downloads

- **[Download Miner Dash for Windows](https://github.com/OBitsPlease/Miner-Dash/releases/download/v0.2.0-beta.1/miningdash-windows-installer.zip)**
- **[Download the Miner Dash Rig OS USB image](https://github.com/OBitsPlease/Miner-Dash/releases/download/v0.2.0-beta.1/minerdash-rig-os.img.xz)**
- [View the complete release and checksums](https://github.com/OBitsPlease/Miner-Dash/releases/tag/v0.2.0-beta.1)

The Windows installer creates a new local controller with unique credentials
and an empty database. The Rig OS image can run persistently from a USB drive or
install itself to an internal drive through its guided console menu.

Before flashing or installing:

- Back up anything important on the target USB drive or internal disk.
- Verify downloads against the published SHA-256 files.
- Expect beta defects and report reproducible issues through GitHub Issues.
- Do not expose the controller directly to the internet. Use the included
  owner-specific Cloudflare tunnel setup or another authenticated private
  access method.

Miner Dash does not include a mandatory mining fee.
