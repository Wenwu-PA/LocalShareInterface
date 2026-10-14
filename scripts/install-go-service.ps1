[CmdletBinding(SupportsShouldProcess=$true)]
param(
    [Parameter(Mandatory=$true)][string]$Binary,
    [Parameter(Mandatory=$true)][string]$Config
)
$ErrorActionPreference = 'Stop'
$binaryPath = (Resolve-Path -LiteralPath $Binary).ProviderPath
$configPath = (Resolve-Path -LiteralPath $Config).ProviderPath
if ((Get-Item -LiteralPath $binaryPath).PSIsContainer -or (Get-Item -LiteralPath $configPath).PSIsContainer) { throw 'Нужны пути к файлам бинарника и конфигурации.' }
if ($binaryPath.Contains('"') -or $configPath.Contains('"')) { throw 'Кавычки в путях не поддерживаются.' }
if (Get-Service -Name 'LANBridgeGo' -ErrorAction SilentlyContinue) { throw 'LANBridgeGo уже существует. Остановите службу и обновите бинарник либо удалите службу отдельным скриптом.' }
$command = '"' + $binaryPath + '" -config "' + $configPath + '"'
if ($PSCmdlet.ShouldProcess('LANBridgeGo', 'Создать службу LocalService (без запуска)')) {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Установка службы требует PowerShell от администратора.' }
    $credential = [System.Management.Automation.PSCredential]::new('NT AUTHORITY\LocalService', [Security.SecureString]::new())
    New-Service -Name 'LANBridgeGo' -DisplayName 'LANBridge Go' -BinaryPathName $command -StartupType Automatic -Credential $credential -Description 'LANBridge: локальные файлы и мониторинг (Go)' | Out-Null
    Write-Output 'Служба создана, но не запущена. Предоставьте LocalService чтение бинарника/config/.env.go и изменение только runtime/общей папки, затем Start-Service LANBridgeGo.'
}
