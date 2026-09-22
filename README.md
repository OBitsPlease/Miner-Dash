# Miner Dash

Miner Dash is a self-hosted mining controller and persistent Linux rig operating
system. It is designed to manage rigs without requiring Hive OS.

## Beta testing

The current beta is intended for testers. Download the Windows installer and
Rig OS image from the repository's
[Releases](https://github.com/OBitsPlease/Miner-Dash/releases) page.

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
