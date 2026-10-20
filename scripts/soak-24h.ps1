param(
    [string]$Binary = '',
    [string]$Python = '',
    [int]$Seconds = 86400
)
$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
if (-not $Binary) { $Binary = Join-Path $repo 'dist/lanbridge.exe' }
if (-not $Python) {
    $Python = Join-Path $repo '.venv/Scripts/python.exe'
    if (-not (Test-Path -LiteralPath $Python)) { $Python = 'python' }
}
& $Python (Join-Path $PSScriptRoot 'verify_go_soak.py') --binary $Binary --seconds $Seconds --monitors 50 --sample-seconds 60 --file-mib 8 --pause 5
exit $LASTEXITCODE
