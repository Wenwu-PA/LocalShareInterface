[CmdletBinding(SupportsShouldProcess=$true)]
param()
$ErrorActionPreference = 'Stop'
$service = Get-Service -Name 'LANBridgeGo' -ErrorAction SilentlyContinue
if (-not $service) { Write-Output 'LANBridgeGo не установлена.'; return }
if ($PSCmdlet.ShouldProcess('LANBridgeGo', 'Остановить и удалить службу; файлы останутся')) {
    Stop-Service -Name 'LANBridgeGo' -ErrorAction Stop
    $service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(40))
    & "$env:SystemRoot\System32\sc.exe" delete LANBridgeGo
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось удалить регистрацию службы.' }
}
