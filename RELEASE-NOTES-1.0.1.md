# Miner Dash 1.0.1 beta

Miner Dash 1.0.1 remains a beta prerelease for community testing.

## Dashboard

- Introduces a compact black and neutral-gray interface throughout the app.
- Adds slim, staged worker cards with click-to-expand details.
- Adds temperature-colored GPU and CPU thread fan indicators.
- Adds compact, expandable GPU and CPU flight sheets, farms, wallets, resources,
  custom miners, and Rig OS setup surfaces.
- Adds responsive navigation and mobile layouts.
- Adds a Discord community link for questions, issue reports, and feature ideas.

## Updates

- Adds a top-right update indicator that turns orange when a verified release is
  available.
- Checks GitHub periodically while the dashboard is open.
- Downloads checksum-pinned controller and agent artifacts before invoking the
  installed updater.
- Preserves controller state, accounts, farms, wallets, pools, flight sheets,
  and telemetry during updates.

## Fresh installations

- Adds starter GPU Farm and CPU Farm groups to help new users organize rigs.
- Existing installations are not backfilled, and deleted starter farms remain
  deleted.
- Fixes Windows service setup for both fresh installs and reinstalls by
  creating fresh services through PowerShell, reconfiguring existing services
  through the Windows service API, and reporting service-control failures
  directly.

## Beta notice

This release is still under active beta testing. Back up important controller
data before testing upgrades and report issues through the Miner Dash community.
