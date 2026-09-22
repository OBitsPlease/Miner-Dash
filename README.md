# Miner Dash

Miner Dash is a self-hosted mining controller and persistent Linux rig operating
system. It is designed to manage rigs without requiring Hive OS.

## Beta testing

The current beta is intended for testers.

### Downloads

- **[Download Miner Dash for Windows](https://github.com/OBitsPlease/Miner-Dash/releases/download/v0.2.0-beta.3/miningdash-windows-installer.zip)**
- [View the complete release and checksums](https://github.com/OBitsPlease/Miner-Dash/releases/tag/v0.2.0-beta.3)

The Windows installer creates a new local controller with unique credentials
and an empty database. The Rig OS image can run persistently from a USB drive or
install itself to an internal drive through its guided console menu.

After installing the Windows controller, open **Rig OS & USB** in the dashboard.
Beginner mode writes and verifies a selected removable USB directly. Advanced
mode provides the image, checksums, and balenaEtcher link. Both modes can pair
the USB with that controller for automatic first-boot enrollment. A completed
installer USB can later be restored to one full-size exFAT partition from the
same page.

Before flashing or installing:

- Back up anything important on the target USB drive or internal disk.
- Verify downloads against the published SHA-256 files.
- Expect beta defects and report reproducible issues through GitHub Issues.
- The controller PC can remain connected to the internet normally. Do **not**
  configure router port forwarding that exposes Miner Dash TCP port `8443`
  directly to the public internet. For access while away from home, use the
  included owner-specific Cloudflare Tunnel setup or another authenticated
  private-access method.

Miner Dash does not include a mandatory mining fee.
