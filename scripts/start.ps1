$ErrorActionPreference = 'Stop'
& python (Join-Path $PSScriptRoot '..\run.py') @args
exit $LASTEXITCODE
