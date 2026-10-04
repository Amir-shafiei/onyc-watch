param([switch]$Check, [switch]$Preview)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

# Reuse the user's existing Windows proxy without changing system settings.
if (-not $env:HTTPS_PROXY) {
    $proxySettings = Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Internet Settings' -ErrorAction SilentlyContinue
    if ($proxySettings.ProxyEnable -eq 1 -and $proxySettings.ProxyServer) {
        $proxyAddress = [string]$proxySettings.ProxyServer
        if ($proxyAddress.Contains('=')) {
            $httpsPart = $proxyAddress.Split(';') | Where-Object { $_.StartsWith('https=') } | Select-Object -First 1
            if ($httpsPart) { $proxyAddress = $httpsPart.Substring(6) } else { $proxyAddress = '' }
        }
        if ($proxyAddress) {
            if (-not $proxyAddress.Contains('://')) { $proxyAddress = 'http://' + $proxyAddress }
            $env:HTTPS_PROXY = $proxyAddress
            Write-Host 'Using the existing Windows HTTPS proxy.'
        }
    }
}

if (-not $Check -and -not $Preview -and -not $env:TELEGRAM_BOT_TOKEN -and -not (Test-Path -LiteralPath '.env')) {
    Write-Host 'Create your Telegram bot with @BotFather, then enter its token below.'
    $secureToken = Read-Host 'Telegram bot token (hidden)' -AsSecureString
    $tokenPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureToken)
    try {
        $botToken = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($tokenPointer)
        if ($botToken -notmatch '^\d+:[A-Za-z0-9_-]+$') { throw 'Invalid token format.' }
        $settings = Get-Content -LiteralPath '.env.example' -Raw
        $settings = $settings.Replace('TELEGRAM_BOT_TOKEN=', 'TELEGRAM_BOT_TOKEN=' + $botToken)
        [IO.File]::WriteAllText((Join-Path $PSScriptRoot '.env'), $settings, [Text.UTF8Encoding]::new($false))
        Write-Host 'Saved locally in .env. Do not share that file.'
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($tokenPointer)
        $botToken = $null
        $secureToken.Dispose()
    }
}

$botArguments = @()
if ($Check) { $botArguments += '-check' }
if ($Preview) { $botArguments += '-preview' }
$executable = Join-Path $PSScriptRoot 'bin\onyc-watch-v1.7.exe'
if (-not (Test-Path -LiteralPath $executable)) { $executable = Join-Path $PSScriptRoot 'bin\onyc-watch.exe' }
if (Test-Path -LiteralPath $executable) { & $executable @botArguments }
elseif (Get-Command go -ErrorAction SilentlyContinue) { & go run . @botArguments }
else { throw 'Install Go 1.24+ or use the included Windows executable.' }
exit $LASTEXITCODE
