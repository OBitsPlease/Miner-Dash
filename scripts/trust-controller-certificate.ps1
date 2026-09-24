[CmdletBinding()]
param(
    [string]$CertificatePath = "$env:ProgramData\MinerDash\tls.crt"
)

$ErrorActionPreference = 'Stop'
if (-not (Test-Path -LiteralPath $CertificatePath -PathType Leaf)) {
    throw "Certificate not found: $CertificatePath"
}

$certificate = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new($CertificatePath)
Write-Host "Subject: $($certificate.Subject)"
Write-Host "SHA-256 certificate trust should only be granted on controller administration PCs."
$confirmation = Read-Host 'Type TRUST to add this certificate to the LocalMachine root store'
if ($confirmation -cne 'TRUST') {
    throw 'Certificate trust was not changed.'
}

$store = [System.Security.Cryptography.X509Certificates.X509Store]::new(
    [System.Security.Cryptography.X509Certificates.StoreName]::Root,
    [System.Security.Cryptography.X509Certificates.StoreLocation]::LocalMachine
)
$store.Open([System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
try {
    $store.Add($certificate)
} finally {
    $store.Close()
}
Write-Host 'MinerDash controller certificate trusted for this computer.'
