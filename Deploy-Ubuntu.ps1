param(
    [ValidatePattern('^[0-9a-zA-Z.-]+$')][string]$Server = '217.60.36.246',
    [ValidateRange(1,65535)][int]$Port = 22
)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
$archive = Join-Path $PSScriptRoot 'server\onyc-watch-ubuntu.tar.gz'
$config = Join-Path $PSScriptRoot '.env'
$state = Join-Path $PSScriptRoot 'data\state.json'
if (-not (Test-Path -LiteralPath $archive)) { throw 'Missing server deployment package.' }
if (-not (Test-Path -LiteralPath $config)) { throw 'Missing local .env configuration.' }
Write-Host 'Stop the Windows bot with Ctrl+C before continuing.'
Read-Host 'Press Enter after stopping the Windows bot' | Out-Null
$stage = '/root/onyc-watch-upload-' + [Guid]::NewGuid().ToString('N')
Write-Host 'SSH will ask for the server password. Input is hidden.'
Write-Host 'On the first connection, verify the host fingerprint with the server console before accepting it.'
& ssh -p $Port -o ConnectTimeout=12 "root@$Server" "umask 077; mkdir '$stage'"
if ($LASTEXITCODE -ne 0) { throw 'SSH connection failed. Check the server, port and login.' }
& scp -P $Port $archive "root@${Server}:$stage/package.tar.gz"
if ($LASTEXITCODE -ne 0) { throw 'Package upload failed.' }
& scp -P $Port $config "root@${Server}:$stage/upload.env"
if ($LASTEXITCODE -ne 0) { throw 'Configuration upload failed.' }
if (Test-Path -LiteralPath $state) {
    & scp -P $Port $state "root@${Server}:$stage/upload-state.json"
    if ($LASTEXITCODE -ne 0) { throw 'State upload failed.' }
}
& ssh -p $Port "root@$Server" "cd '$stage' && tar -xzf package.tar.gz && bash install.sh"
if ($LASTEXITCODE -ne 0) { throw 'Installation failed. See the server output above.' }
Write-Host 'Deployment complete. You can close this window. Keep the Windows bot stopped.'
