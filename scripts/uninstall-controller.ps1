[CmdletBinding()]
param(
    [switch]$RemoveData
)

$ErrorActionPreference = 'Stop'
$service = Get-Service -Name MinerDash -ErrorAction SilentlyContinue
if ($service) {
    if ($service.Status -ne 'Stopped') {
        Stop-Service -Name MinerDash
    }
    $tunnelService = Get-Service -Name MinerDashCloudflare -ErrorAction SilentlyContinue
    if ($tunnelService) {
        if ($tunnelService.Status -ne 'Stopped') {
            Stop-Service -Name MinerDashCloudflare
        }
        & sc.exe delete MinerDashCloudflare | Out-Null
    }
    Get-NetFirewallRule -DisplayName 'MinerDash Controller LAN' -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    & sc.exe delete MinerDash | Out-Null
}
Remove-Item -LiteralPath "$env:ProgramFiles\MinerDash\minerdash-server.exe" -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath "$env:ProgramFiles\MinerDash\minerdash-updater.exe" -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath "$env:ProgramFiles\MinerDash\miningdash.ico" -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath "$env:ProgramFiles\MinerDash\setup-cloudflare-tunnel.ps1" -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath "$env:ProgramFiles\MinerDash\Setup Miner Dash Remote Access.bat" -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath (Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'Mining Dash.lnk') -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath (Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'Setup Miner Dash Remote Access.lnk') -Force -ErrorAction SilentlyContinue
if ($RemoveData) {
    Remove-Item -LiteralPath "$env:ProgramData\MinerDash" -Recurse -Force -ErrorAction SilentlyContinue
}
