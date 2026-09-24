[CmdletBinding()]
param(
    [string]$Hostname,
    [int]$ControllerPort = 8443
)

$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this setup as an administrator.'
}

if (-not (Get-Command cloudflared.exe -ErrorAction SilentlyContinue)) {
    if (-not (Get-Command winget.exe -ErrorAction SilentlyContinue)) {
        throw 'Windows Package Manager (winget) is required to install Cloudflare Tunnel.'
    }
    Write-Host 'Installing the official Cloudflare Tunnel client...'
    & winget.exe install `
        --exact `
        --id Cloudflare.cloudflared `
        --source winget `
        --accept-package-agreements `
        --accept-source-agreements `
        --disable-interactivity
    if ($LASTEXITCODE -ne 0) {
        throw "Cloudflare Tunnel installation failed (winget exit $LASTEXITCODE)."
    }
    $machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $env:Path = "$machinePath;$userPath;$env:Path"
}
$cloudflared = (Get-Command cloudflared.exe -ErrorAction Stop).Source

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

if (-not $Hostname) {
    Write-Host ''
    Write-Host 'Miner Dash remote access requires a domain managed by the rig owner in Cloudflare.'
    Write-Host 'A free Cloudflare account can be created in the browser, but domain registration may have a separate cost.'
    Start-Process 'https://dash.cloudflare.com/sign-up'
    Read-Host 'Create or sign in to the owner account, add the owner domain, then press Enter'
    $Hostname = Read-Host 'Remote Miner Dash hostname (example: miners.example.com)'
}
$Hostname = $Hostname.Trim().ToLowerInvariant()
if ($Hostname -notmatch '^[a-z0-9](?:[a-z0-9.-]{1,251})[a-z0-9]$' -or $Hostname -notmatch '\.') {
    throw 'Hostname must be a valid DNS name controlled by this Cloudflare account.'
}
if ($ControllerPort -lt 1 -or $ControllerPort -gt 65535) {
    throw 'ControllerPort must be between 1 and 65535.'
}

Write-Host 'Opening Cloudflare authorization. Sign in as the owner and authorize the correct domain.'
& $cloudflared tunnel login
if ($LASTEXITCODE -ne 0) {
    throw 'Cloudflare authorization failed.'
}

$tunnelName = ('minerdash-' + $env:COMPUTERNAME.ToLowerInvariant()) -replace '[^a-z0-9-]', '-'
$existing = @(& $cloudflared tunnel list --output json | ConvertFrom-Json) |
    Where-Object { $_.name -eq $tunnelName } |
    Select-Object -First 1
if ($existing) {
    $tunnelID = [string]$existing.id
    Write-Host "Using existing owner tunnel $tunnelName ($tunnelID)."
} else {
    & $cloudflared tunnel create $tunnelName
    if ($LASTEXITCODE -ne 0) {
        throw 'Cloudflare tunnel creation failed.'
    }
    $created = @(& $cloudflared tunnel list --output json | ConvertFrom-Json) |
        Where-Object { $_.name -eq $tunnelName } |
        Select-Object -First 1
    if (-not $created) {
        throw 'Cloudflare created the tunnel but its identifier could not be found.'
    }
    $tunnelID = [string]$created.id
}

$sourceCredential = Join-Path $env:USERPROFILE ".cloudflared\$tunnelID.json"
if (-not (Test-Path -LiteralPath $sourceCredential -PathType Leaf)) {
    throw "Tunnel credential was not created at $sourceCredential."
}
$configurationDirectory = Join-Path $env:ProgramData 'MinerDash\cloudflared'
New-Item -ItemType Directory -Force -Path $configurationDirectory | Out-Null
& icacls.exe $configurationDirectory /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
$credential = Join-Path $configurationDirectory "$tunnelID.json"
Copy-Item -LiteralPath $sourceCredential -Destination $credential -Force
$configPath = Join-Path $configurationDirectory 'config.yml'
@"
tunnel: $tunnelID
credentials-file: $($credential -replace '\\','/')
ingress:
  - hostname: $Hostname
    service: https://localhost:$ControllerPort
    originRequest:
      noTLSVerify: true
  - service: http_status:404
"@ | Set-Content -LiteralPath $configPath -Encoding utf8

& $cloudflared tunnel route dns $tunnelID $Hostname
if ($LASTEXITCODE -ne 0) {
    throw 'Cloudflare DNS route creation failed.'
}

$serviceName = 'MinerDashCloudflare'
$existingService = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
if ($existingService) {
    if ($existingService.Status -ne 'Stopped') {
        Stop-Service -Name $serviceName
        $existingService.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(45))
    }
}
$binaryPath = "`"$cloudflared`" --no-autoupdate --config `"$configPath`" tunnel run"
if ($existingService) {
    $serviceConfiguration = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'" -ErrorAction Stop
    $change = Invoke-CimMethod -InputObject $serviceConfiguration -MethodName Change -Arguments @{
        DisplayName = 'Miner Dash Cloudflare Tunnel'
        PathName    = $binaryPath
        StartMode   = 'Automatic'
    }
    if ($change.ReturnValue -ne 0) {
        throw "Update $serviceName service failed (Win32_Service.Change returned $($change.ReturnValue))."
    }
} else {
    $existingService = New-Service `
        -Name $serviceName `
        -BinaryPathName $binaryPath `
        -DisplayName 'Miner Dash Cloudflare Tunnel' `
        -Description 'Owner-authorized private tunnel to the local Miner Dash controller' `
        -StartupType Automatic
}
Invoke-ServiceControl -Action "Set $serviceName service description" -Arguments @(
    'description', $serviceName, 'Owner-authorized private tunnel to the local Miner Dash controller'
)
Invoke-ServiceControl -Action "Set $serviceName service recovery policy" -Arguments @(
    'failure', $serviceName,
    'reset= 86400',
    'actions= restart/5000/restart/15000/none/0'
)
$cloudflareService = Get-Service -Name $serviceName -ErrorAction Stop
Start-Service -InputObject $cloudflareService
$cloudflareService.WaitForStatus('Running', [TimeSpan]::FromSeconds(45))

Write-Host ''
Write-Host "Owner-specific Miner Dash remote URL: https://$Hostname" -ForegroundColor Green
Write-Host 'This installation uses only the Cloudflare account and domain authorized during setup.'
