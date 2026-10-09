$ErrorActionPreference = 'Stop'
& python (Join-Path $PSScriptRoot '..\run.py') --install-only @args
exit $LASTEXITCODE
