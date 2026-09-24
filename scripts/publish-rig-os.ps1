[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$ImagePath,
    [string]$BuildManifestPath = '',
    [string]$DataDirectory = "$env:ProgramData\MinerDash"
)

$ErrorActionPreference = 'Stop'
$image = Get-Item -LiteralPath $ImagePath -ErrorAction Stop
if ($image.PSIsContainer -or $image.Length -eq 0) {
    throw 'ImagePath must identify a non-empty Rig OS image.'
}
if (-not $BuildManifestPath) {
    $BuildManifestPath = Join-Path $image.DirectoryName 'minerdash-rig-os.raw.build-manifest'
}
$buildManifest = Get-Item -LiteralPath $BuildManifestPath -ErrorAction Stop
if ($buildManifest.PSIsContainer -or $buildManifest.Length -eq 0) {
    throw 'BuildManifestPath must identify a non-empty Rig OS build manifest.'
}

$rigOSDirectory = Join-Path $DataDirectory 'rig-os'
$destination = Join-Path $rigOSDirectory 'minerdash-rig-os.img.xz'
$sidecar = "$destination.sha256"
$manifestDestination = Join-Path $rigOSDirectory 'minerdash-rig-os.raw.build-manifest'
$stagedImage = "$destination.upload"
$stagedSidecar = "$sidecar.upload"
$stagedManifest = "$manifestDestination.upload"

New-Item -ItemType Directory -Force -Path $rigOSDirectory | Out-Null
try {
    Copy-Item -LiteralPath $image.FullName -Destination $stagedImage -Force
    Copy-Item -LiteralPath $buildManifest.FullName -Destination $stagedManifest -Force
    $digest = (Get-FileHash -LiteralPath $stagedImage -Algorithm SHA256).Hash.ToLowerInvariant()
    "$digest  minerdash-rig-os.img.xz" | Set-Content -LiteralPath $stagedSidecar -Encoding ascii

    Remove-Item -LiteralPath $sidecar -Force -ErrorAction SilentlyContinue
    Move-Item -LiteralPath $stagedImage -Destination $destination -Force
    Move-Item -LiteralPath $stagedSidecar -Destination $sidecar -Force
    Move-Item -LiteralPath $stagedManifest -Destination $manifestDestination -Force
} finally {
    Remove-Item -LiteralPath $stagedImage, $stagedSidecar, $stagedManifest -Force -ErrorAction SilentlyContinue
}

Write-Host "Published $destination"
Write-Host "SHA-256: $digest"
