[CmdletBinding()]
param(
    [string]$DataDirectory = "$env:ProgramData\MinerDash",
    [string]$Destination = (Join-Path $PWD "minerdash-backup-$(Get-Date -Format 'yyyyMMdd-HHmmss').zip")
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $DataDirectory -PathType Container)) {
    throw "MinerDash data directory not found: $DataDirectory"
}

$service = Get-Service -Name MinerDash -ErrorAction SilentlyContinue
$restart = $service -and $service.Status -eq 'Running'
if ($restart) {
    Stop-Service -Name MinerDash
}
try {
    Compress-Archive -Path (Join-Path $DataDirectory '*') -DestinationPath $Destination -CompressionLevel Optimal -Force
} finally {
    if ($restart) {
        Start-Service -Name MinerDash
    }
}
Write-Host "Backup created: $Destination"
