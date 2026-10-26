# NodeHub one-command Docker install for Windows.
# Run from the repository root: .\install.ps1
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
  throw 'Docker Desktop is required: https://docs.docker.com/desktop/'
}

function New-RandomHex([int]$bytes) {
  $buffer = New-Object byte[] $bytes
  [System.Security.Cryptography.RandomNumberGenerator]::Fill($buffer)
  return ($buffer | ForEach-Object { $_.ToString('x2') }) -join ''
}

if (-not (Test-Path '.env')) {
  $password = 'NodeHub-' + (New-RandomHex 6)
  @"
NODEHUB_PUBLIC_URL=http://localhost:8080
AGENT_TOKEN=$(New-RandomHex 24)
ADMIN_USERNAME=admin
ADMIN_PASSWORD=$password
JWT_SECRET=$(New-RandomHex 32)
"@ | Set-Content -Encoding ASCII .env
  Write-Host 'Created .env with random credentials.'
} else {
  $password = (Get-Content .env | Where-Object { $_ -match '^ADMIN_PASSWORD=' } | Select-Object -First 1) -replace '^ADMIN_PASSWORD=', ''
}

docker compose up -d --build

Write-Host ''
Write-Host 'NodeHub is starting.'
Write-Host 'URL:  http://localhost:8080'
Write-Host 'User: admin'
Write-Host "Pass: $password"
