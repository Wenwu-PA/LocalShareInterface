param(
    [string]$GoExe = $env:LANBRIDGE_GO_TOOLCHAIN,
    [ValidateSet('windows','linux')][string]$Target = 'windows',
    [ValidateSet('amd64','arm64')][string]$Arch = 'amd64'
)
$ErrorActionPreference = 'Stop'
$projectRoot = $PSScriptRoot
if (-not $GoExe) {
    $goCommand = Get-Command go -ErrorAction SilentlyContinue
    if ($goCommand) { $GoExe = $goCommand.Source }
    else { $GoExe = Join-Path $env:USERPROFILE '.cache\lanbridge-toolchain\go\bin\go.exe' }
}
if (-not (Test-Path -LiteralPath $GoExe)) { throw 'Установите Go SDK и добавьте go в PATH либо задайте LANBRIDGE_GO_TOOLCHAIN.' }
$oldLocation = Get-Location
$oldOS, $oldArch, $oldCGO = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
try {
    Set-Location (Join-Path $projectRoot 'web')
    & npm.cmd ci --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось установить frontend-зависимости' }
    & npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw 'Ошибка сборки интерфейса' }
    [System.IO.File]::WriteAllText((Join-Path $projectRoot 'web\dist\.gitkeep'), '')
    Set-Location $projectRoot
    $env:GOOS = $Target
    $env:GOARCH = $Arch
    $env:CGO_ENABLED = '0'
    $name = if ($Target -eq 'windows') { 'lanbridge.exe' } else { "lanbridge-linux-$Arch" }
    & $GoExe build -trimpath -ldflags '-s -w' -o (Join-Path 'dist' $name) ./cmd/lanbridge
    if ($LASTEXITCODE -ne 0) { throw 'Ошибка сборки Go' }
    $result = Get-Item -LiteralPath (Join-Path $projectRoot "dist\$name")
    Write-Output "Готово: $($result.FullName) ($($result.Length) байт)"
} finally {
    Set-Location $oldLocation
    $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $oldOS, $oldArch, $oldCGO
}
