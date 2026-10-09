$ErrorActionPreference = 'Stop'
& python (Join-Path $PSScriptRoot '..\run.py') --cert @args
exit $LASTEXITCODE
