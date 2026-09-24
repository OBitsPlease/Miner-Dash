[CmdletBinding()]
param(
    [string]$InstallDirectory = "$env:ProgramFiles\MinerDash",
    [string]$DataDirectory = "$env:ProgramData\MinerDash",
    [int]$Port = 8443,
    [switch]$NoSplash
)

$ErrorActionPreference = 'Stop'
$source = Join-Path $PSScriptRoot '..\build\minerdash-server.exe'
$updaterSource = Join-Path $PSScriptRoot '..\build\minerdash-updater.exe'
$agentAMD64Source = Join-Path $PSScriptRoot '..\build\minerdash-agent-amd64'
$agentARM64Source = Join-Path $PSScriptRoot '..\build\minerdash-agent-arm64'
$brandingDirectory = Join-Path $PSScriptRoot '..\assets\branding'
$splashPath = Join-Path $brandingDirectory 'miningdash-installer-splash.png'
$iconSource = Join-Path $brandingDirectory 'miningdash.ico'
$cloudflareSetupSource = Join-Path $PSScriptRoot 'setup-cloudflare-tunnel.ps1'
$cloudflareBatchSource = Join-Path $PSScriptRoot 'Setup Miner Dash Remote Access.bat'
$rigOSSourceDirectory = @(
    (Join-Path $PSScriptRoot '..\rig-os'),
    (Join-Path $PSScriptRoot '..\build\rig-os')
) | Where-Object { Test-Path -LiteralPath $_ -PathType Container } | Select-Object -First 1
$rigOSArtifacts = @(
    'minerdash-rig-os.img.xz',
    'minerdash-rig-os.img.xz.sha256',
    'minerdash-rig-os.raw.build-manifest'
)
if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
    throw "Build the controller first: go build -o build\minerdash-server.exe .\cmd\minerdash-server"
}
if (-not (Test-Path -LiteralPath $updaterSource -PathType Leaf)) {
    throw 'Automatic updater is missing from the installer.'
}
if (-not (Test-Path -LiteralPath $iconSource -PathType Leaf)) {
    throw "Desktop icon is missing: $iconSource"
}
if (-not (Test-Path -LiteralPath $agentAMD64Source -PathType Leaf) -or
    -not (Test-Path -LiteralPath $agentARM64Source -PathType Leaf)) {
    throw 'Linux agent releases are missing from the installer.'
}
if (-not $rigOSSourceDirectory) {
    throw 'Rig OS is missing from the installer.'
}
foreach ($name in $rigOSArtifacts) {
    if (-not (Test-Path -LiteralPath (Join-Path $rigOSSourceDirectory $name) -PathType Leaf)) {
        throw "Rig OS installer artifact is missing: $name"
    }
}
$rigOSImageSource = Join-Path $rigOSSourceDirectory 'minerdash-rig-os.img.xz'
$expectedRigOSHash = ((Get-Content -LiteralPath "$rigOSImageSource.sha256" -Raw) -split '\s+')[0].ToLowerInvariant()
if ($expectedRigOSHash -notmatch '^[0-9a-f]{64}$') {
    throw 'Rig OS checksum file is invalid.'
}
$actualRigOSHash = (Get-FileHash -LiteralPath $rigOSImageSource -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualRigOSHash -ne $expectedRigOSHash) {
    throw 'Rig OS image checksum verification failed.'
}
if ($Port -lt 1 -or $Port -gt 65535) {
    throw 'Port must be between 1 and 65535.'
}

function Show-MinerDashSplash {
    if ($NoSplash -or -not [Environment]::UserInteractive -or -not (Test-Path -LiteralPath $splashPath -PathType Leaf)) {
        return $null
    }
    Add-Type -AssemblyName System.Drawing
    Add-Type -AssemblyName System.Windows.Forms
    $form = New-Object System.Windows.Forms.Form
    $form.Text = 'Installing Mining Dash'
    $form.StartPosition = 'CenterScreen'
    $form.FormBorderStyle = 'FixedSingle'
    $form.MaximizeBox = $false
    $form.MinimizeBox = $false
    $form.ShowInTaskbar = $true
    $form.BackColor = [System.Drawing.Color]::Black
    $form.ClientSize = New-Object System.Drawing.Size 960,578
    $form.Icon = New-Object System.Drawing.Icon $iconSource

    $image = [System.Drawing.Image]::FromFile($splashPath)
    $picture = New-Object System.Windows.Forms.PictureBox
    $picture.Dock = 'Fill'
    $picture.Image = $image
    $picture.SizeMode = 'Zoom'
    $form.Controls.Add($picture)
    $form.Tag = @{ Image = $image; Icon = $form.Icon }
    $form.Show()
    $form.Refresh()
    return $form
}

function Close-MinerDashSplash($Form) {
    if ($null -eq $Form) {
        return
    }
    $resources = $Form.Tag
    $Form.Close()
    $Form.Dispose()
    $resources.Image.Dispose()
    $resources.Icon.Dispose()
}

function Invoke-ServiceControl {
    param(
        [Parameter(Mandatory)]
        [string[]]$Arguments,
        [Parameter(Mandatory)]
        [string]$Action
    )

    $output = & sc.exe @Arguments 2>&1
    if ($LASTEXITCODE -ne 0) {
        throw "$Action failed (sc.exe exit $LASTEXITCODE): $($output -join ' ')"
    }
}

$splash = Show-MinerDashSplash
try {
    New-Item -ItemType Directory -Force -Path $InstallDirectory, $DataDirectory | Out-Null
    $executable = Join-Path $InstallDirectory 'minerdash-server.exe'
    $installedIcon = Join-Path $InstallDirectory 'miningdash.ico'
    $installedUpdater = Join-Path $InstallDirectory 'minerdash-updater.exe'
    $agentReleaseDirectory = Join-Path $DataDirectory 'agent-releases'
    $rigOSDirectory = Join-Path $DataDirectory 'rig-os'
    New-Item -ItemType Directory -Force -Path $agentReleaseDirectory, $rigOSDirectory | Out-Null

    $existing = Get-Service -Name MinerDash -ErrorAction SilentlyContinue
    if ($existing) {
        if ($existing.Status -ne 'Stopped') {
            Stop-Service -Name MinerDash
            $existing.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(45))
        }
    }

    & icacls.exe $DataDirectory /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
    Copy-Item -LiteralPath $source -Destination $executable -Force
    Copy-Item -LiteralPath $updaterSource -Destination $installedUpdater -Force
    Copy-Item -LiteralPath $iconSource -Destination $installedIcon -Force
    Copy-Item -LiteralPath $cloudflareSetupSource -Destination (Join-Path $InstallDirectory 'setup-cloudflare-tunnel.ps1') -Force
    Copy-Item -LiteralPath $cloudflareBatchSource -Destination (Join-Path $InstallDirectory 'Setup Miner Dash Remote Access.bat') -Force
    Copy-Item -LiteralPath $agentAMD64Source -Destination (Join-Path $agentReleaseDirectory 'minerdash-agent-amd64') -Force
    Copy-Item -LiteralPath $agentARM64Source -Destination (Join-Path $agentReleaseDirectory 'minerdash-agent-arm64') -Force
    foreach ($name in $rigOSArtifacts) {
        Copy-Item -LiteralPath (Join-Path $rigOSSourceDirectory $name) -Destination (Join-Path $rigOSDirectory $name) -Force
    }

    $binaryPath = "`"$executable`" -listen :$Port -data `"$DataDirectory`""
    if ($existing) {
        $serviceConfiguration = Get-CimInstance -ClassName Win32_Service -Filter "Name='MinerDash'" -ErrorAction Stop
        $change = Invoke-CimMethod -InputObject $serviceConfiguration -MethodName Change -Arguments @{
            DisplayName = 'Mining Dash Controller'
            PathName    = $binaryPath
            StartMode   = 'Automatic'
        }
        if ($change.ReturnValue -ne 0) {
            throw "Update MinerDash service failed (Win32_Service.Change returned $($change.ReturnValue))."
        }
    } else {
        $existing = New-Service `
            -Name MinerDash `
            -BinaryPathName $binaryPath `
            -DisplayName 'Mining Dash Controller' `
            -Description 'Local CPU and GPU mining control plane' `
            -StartupType Automatic
    }
    Invoke-ServiceControl -Action 'Set MinerDash service description' -Arguments @(
        'description', 'MinerDash', 'Local CPU and GPU mining control plane'
    )
    Invoke-ServiceControl -Action 'Set MinerDash service recovery policy' -Arguments @(
        'failure', 'MinerDash',
        'reset= 86400',
        'actions= restart/5000/restart/15000/none/0'
    )
    Get-NetFirewallRule -DisplayName 'MinerDash Controller LAN' -ErrorAction SilentlyContinue |
        Remove-NetFirewallRule
    New-NetFirewallRule `
        -DisplayName 'MinerDash Controller LAN' `
        -Description 'Allow Miner Dash rigs on private networks to reach the local controller.' `
        -Direction Inbound `
        -Action Allow `
        -Protocol TCP `
        -LocalPort $Port `
        -Profile Private | Out-Null
    $service = Get-Service -Name MinerDash -ErrorAction Stop
    Start-Service -InputObject $service
    $service.WaitForStatus('Running', [TimeSpan]::FromSeconds(45))

    $shortcutPath = Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'Mining Dash.lnk'
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = "$env:WINDIR\explorer.exe"
    $shortcut.Arguments = "https://localhost:$Port"
    $shortcut.WorkingDirectory = $InstallDirectory
    $shortcut.IconLocation = "$installedIcon,0"
    $shortcut.Description = 'Open the local Mining Dash dashboard'
    $shortcut.Save()
    $remoteShortcutPath = Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'Setup Miner Dash Remote Access.lnk'
    $remoteShortcut = $shell.CreateShortcut($remoteShortcutPath)
    $remoteShortcut.TargetPath = Join-Path $InstallDirectory 'Setup Miner Dash Remote Access.bat'
    $remoteShortcut.WorkingDirectory = $InstallDirectory
    $remoteShortcut.IconLocation = "$installedIcon,0"
    $remoteShortcut.Description = 'Connect Miner Dash through the owner Cloudflare account'
    $remoteShortcut.Save()

    Write-Host "Mining Dash is running at https://localhost:$Port"
    $lanAddresses = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
        Where-Object {
            $_.IPAddress -ne '127.0.0.1' -and
            $_.PrefixOrigin -ne 'WellKnown' -and
            $_.AddressState -eq 'Preferred'
        } |
        Select-Object -ExpandProperty IPAddress -Unique
    foreach ($address in $lanAddresses) {
        Write-Host "Rig controller URL: https://${address}:$Port"
    }
    Write-Host "Desktop shortcut created at $shortcutPath"
    $secretsPath = Join-Path $DataDirectory 'secrets.json'
    for ($attempt = 0; $attempt -lt 20 -and -not (Test-Path -LiteralPath $secretsPath); $attempt++) {
        Start-Sleep -Milliseconds 250
    }
    if (-not (Test-Path -LiteralPath $secretsPath -PathType Leaf)) {
        throw "Miner Dash started but did not create $secretsPath."
    }
    $secrets = Get-Content -LiteralPath $secretsPath -Raw | ConvertFrom-Json
    Write-Host ''
    Write-Host 'SAVE THESE FIRST-LOGIN CREDENTIALS:' -ForegroundColor Yellow
    Write-Host "Administrator token: $($secrets.admin_token)"
    Write-Host "Enrollment token:    $($secrets.enrollment_token)"
    Write-Host "Recovery code:       $($secrets.recovery_code)"
    Write-Host "Credentials remain stored in $secretsPath"
} finally {
    Close-MinerDashSplash $splash
}
