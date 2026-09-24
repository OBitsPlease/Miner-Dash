[CmdletBinding()]
param(
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$Version = '1.0.2',
    [string]$OutputDirectory = (Join-Path $PSScriptRoot '..\build'),
    [string]$RigOSDirectory = (Join-Path $PSScriptRoot '..\build\rig-os')
)

$ErrorActionPreference = 'Stop'
$linkerFlags = "-s -w -X minerdash/internal/buildinfo.Version=$Version"
$root = Resolve-Path (Join-Path $PSScriptRoot '..')
New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
$resolvedRigOSDirectory = Resolve-Path -LiteralPath $RigOSDirectory -ErrorAction Stop
$rigOSArtifacts = @(
    'minerdash-rig-os.img.xz',
    'minerdash-rig-os.img.xz.sha256',
    'minerdash-rig-os.raw.build-manifest'
)
foreach ($name in $rigOSArtifacts) {
    if (-not (Test-Path -LiteralPath (Join-Path $resolvedRigOSDirectory $name) -PathType Leaf)) {
        throw "Rig OS release artifact is missing: $(Join-Path $resolvedRigOSDirectory $name)"
    }
}

Push-Location $root
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Tests failed.' }

    $serverArtifact = Join-Path $OutputDirectory 'minerdash-server.exe'
    $updaterArtifact = Join-Path $OutputDirectory 'minerdash-updater.exe'
    $amd64Artifact = Join-Path $OutputDirectory 'minerdash-agent-amd64'
    $arm64Artifact = Join-Path $OutputDirectory 'minerdash-agent-arm64'
    $installerArtifact = Join-Path $OutputDirectory 'miningdash-windows-installer.zip'
    $releaseArtifacts = @($serverArtifact, $updaterArtifact, $amd64Artifact, $arm64Artifact, $installerArtifact)

    go build -trimpath -ldflags $linkerFlags -o $serverArtifact .\cmd\minerdash-server
    if ($LASTEXITCODE -ne 0) { throw 'Windows controller build failed.' }
    go build -trimpath -ldflags $linkerFlags -o $updaterArtifact .\cmd\minerdash-updater
    if ($LASTEXITCODE -ne 0) { throw 'Windows updater build failed.' }

    $oldGOOS = $env:GOOS
    $oldGOARCH = $env:GOARCH
    try {
        $env:GOOS = 'linux'
        $env:GOARCH = 'amd64'
        go build -trimpath -ldflags $linkerFlags -o $amd64Artifact .\cmd\minerdash-agent
        if ($LASTEXITCODE -ne 0) { throw 'Linux AMD64 agent build failed.' }
        $env:GOARCH = 'arm64'
        go build -trimpath -ldflags $linkerFlags -o $arm64Artifact .\cmd\minerdash-agent
        if ($LASTEXITCODE -ne 0) { throw 'Linux ARM64 agent build failed.' }
    } finally {
        $env:GOOS = $oldGOOS
        $env:GOARCH = $oldGOARCH
    }

    $installerRoot = Join-Path $OutputDirectory 'windows-installer'
    Remove-Item -LiteralPath $installerRoot -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path `
        (Join-Path $installerRoot 'build'), `
        (Join-Path $installerRoot 'rig-os'), `
        (Join-Path $installerRoot 'scripts'), `
        (Join-Path $installerRoot 'assets\branding') | Out-Null
    Copy-Item -LiteralPath $serverArtifact -Destination (Join-Path $installerRoot 'build\minerdash-server.exe')
    Copy-Item -LiteralPath $updaterArtifact -Destination (Join-Path $installerRoot 'build\minerdash-updater.exe')
    Copy-Item -LiteralPath $amd64Artifact -Destination (Join-Path $installerRoot 'build\minerdash-agent-amd64')
    Copy-Item -LiteralPath $arm64Artifact -Destination (Join-Path $installerRoot 'build\minerdash-agent-arm64')
    Copy-Item -LiteralPath (Join-Path $root 'scripts\install-controller.ps1') -Destination (Join-Path $installerRoot 'scripts')
    Copy-Item -LiteralPath (Join-Path $root 'scripts\install-controller-wizard.ps1') -Destination (Join-Path $installerRoot 'scripts')
    Copy-Item -LiteralPath (Join-Path $root 'scripts\uninstall-controller.ps1') -Destination (Join-Path $installerRoot 'scripts')
    Copy-Item -LiteralPath (Join-Path $root 'scripts\trust-controller-certificate.ps1') -Destination (Join-Path $installerRoot 'scripts')
    Copy-Item -LiteralPath (Join-Path $root 'scripts\setup-cloudflare-tunnel.ps1') -Destination (Join-Path $installerRoot 'scripts')
    Copy-Item -LiteralPath (Join-Path $root 'scripts\Setup Miner Dash Remote Access.bat') -Destination (Join-Path $installerRoot 'scripts')
    Copy-Item -LiteralPath (Join-Path $root 'assets\branding\miningdash.ico') -Destination (Join-Path $installerRoot 'assets\branding')
    Copy-Item -LiteralPath (Join-Path $root 'assets\branding\miningdash-installer-splash.png') -Destination (Join-Path $installerRoot 'assets\branding')
    foreach ($name in $rigOSArtifacts) {
        Copy-Item -LiteralPath (Join-Path $resolvedRigOSDirectory $name) -Destination (Join-Path $installerRoot 'rig-os') -Force
    }
    @'
@echo off
net session >nul 2>&1
if %errorlevel% neq 0 (
  powershell.exe -NoProfile -Command "Start-Process -FilePath '%~f0' -Verb RunAs"
  exit /b
)
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\install-controller.ps1"
if %errorlevel% neq 0 (
  echo.
  echo Mining Dash installation failed.
)
echo.
pause
'@ | Set-Content -LiteralPath (Join-Path $installerRoot 'Install Miner Dash.bat') -Encoding ascii
    @'
MINER DASH TEST INSTALLER

1. Extract this ZIP to a normal folder.
2. Double-click "Install Miner Dash.bat".
3. Approve the Windows administrator prompt.
4. Save the administrator token, enrollment token, and recovery code shown when installation finishes.
5. Open the Mining Dash desktop shortcut.
6. Optional: run scripts\Setup Miner Dash Remote Access.bat and authorize the
   rig owner's own Cloudflare account and domain.
7. Open Rig OS & USB in the dashboard. Beginner mode writes and verifies a
   selected removable USB directly. Advanced mode provides the image and
   balenaEtcher downloads.

The installer adds a private-network firewall rule for TCP port 8443. Do not
change the Windows network to Public if rigs must connect to this controller.
The controller PC can use the internet normally, but do not create a router
port-forward that exposes TCP port 8443 directly to the public internet.
No Cloudflare credentials, Miner Dash users, wallets, flight sheets, IP
addresses, or controller state are included in this installer.
'@ | Set-Content -LiteralPath (Join-Path $installerRoot 'TESTER-README.txt') -Encoding ascii
    Remove-Item -LiteralPath $installerArtifact -Force -ErrorAction SilentlyContinue
    Compress-Archive -Path (Join-Path $installerRoot '*') -DestinationPath $installerArtifact -CompressionLevel Optimal
    Remove-Item -LiteralPath $installerRoot -Recurse -Force

    foreach ($name in $rigOSArtifacts) {
        $sourcePath = Join-Path $resolvedRigOSDirectory $name
        $destinationPath = Join-Path $OutputDirectory $name
        Copy-Item -LiteralPath $sourcePath -Destination $destinationPath -Force
        $releaseArtifacts += $destinationPath
    }

    Get-Item -LiteralPath $releaseArtifacts |
        Get-FileHash -Algorithm SHA256 |
        Select-Object Hash, @{Name='File';Expression={Split-Path $_.Path -Leaf}} |
        ConvertTo-Json |
        Set-Content -LiteralPath (Join-Path $OutputDirectory 'SHA256SUMS.json') -Encoding utf8
} finally {
    Pop-Location
}
