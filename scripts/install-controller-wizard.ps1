[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$Host.UI.RawUI.WindowTitle = 'Miner Dash Setup'

try {
    & (Join-Path $PSScriptRoot 'install-controller.ps1') -NoSplash
    Write-Host ''
    Write-Host 'Miner Dash installation completed successfully.' -ForegroundColor Green
    Write-Host 'Save the credentials shown above before continuing.' -ForegroundColor Yellow
    [void](Read-Host 'Press Enter to finish setup')
    exit 0
} catch {
    Write-Host ''
    Write-Host 'Miner Dash installation failed.' -ForegroundColor Red
    Write-Host $_.Exception.Message -ForegroundColor Red
    [void](Read-Host 'Press Enter to close setup')
    exit 1
}
