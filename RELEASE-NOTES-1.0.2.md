# Miner Dash 1.0.2 beta

Miner Dash 1.0.2 improves the new-rig workflow, navigation, release packaging,
and local development experience.

## Navigation and dashboard

- Consolidates the growing navigation bar into Overview, Farms, Mining,
  Operations, Discover, and System groups.
- Adds cursor-focused animated flyouts with keyboard, touch, mobile, and reduced
  motion support.
- Shows live farm status cards directly in the Farms flyout.
- Uses black and neutral-gray flyout surfaces consistent with the dashboard.
- Displays the running Miner Dash version beside the local-controller status.
- Adds dedicated Mining Software, Mining Pools, Power & Prices, and New Coin
  pages.

## Rig OS and setup

- Replaces the separate Rig Setup page with one guided Rig OS & USB workflow.
- Adds visible three-step USB setup instructions and direct Windows USB writing.
- Automatically selects the controller address used by the default network
  route while retaining an optional multi-network override.
- Adds on-demand enrollment credentials for existing Linux installations.
- Clarifies the difference between installing alongside an existing Linux
  system and completely replacing the current operating system.
- Reports expired browser sessions as authentication errors instead of USB tool
  failures.

## Release reliability

- Requires the Rig OS image, checksum, and raw build manifest during release
  builds.
- Bundles verified Rig OS artifacts in the Windows installer.
- Publishes the complete Rig OS artifact set to the local controller.
- Keeps PowerShell diagnostics separate from USB discovery JSON output.
- Removes private-network examples and external-platform-specific wording from
  project-controlled setup material.

## Beta notice

This release remains under active beta testing. Back up important controller
data before testing upgrades and report issues through the Miner Dash community.
