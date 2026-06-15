# run.ps1 - create venv, install deps, start context service on port 8102
# Windows PowerShell. Console is GBK; no emoji.
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not (Test-Path ".\.venv")) {
    Write-Host "[run] creating venv ..."
    python -m venv .venv
}

Write-Host "[run] installing requirements ..."
.\.venv\Scripts\python.exe -m pip install --upgrade pip
.\.venv\Scripts\python.exe -m pip install -r requirements.txt

# ARK_API_KEY is read from environment. Set it before running for full features:
#   $env:ARK_API_KEY = "your-key"
if (-not $env:ARK_API_KEY) {
    Write-Host "[run] WARNING: ARK_API_KEY not set -> degrade mode (no LLM structuring / no vision)."
}

Write-Host "[run] starting uvicorn on port 8102 ..."
.\.venv\Scripts\python.exe -m uvicorn app:app --host 0.0.0.0 --port 8102
