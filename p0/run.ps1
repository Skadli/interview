# P0 一键启动（阶段A：最小依赖 + mock，先在电脑 localhost 验证管道）
# 用法：在 p0 目录下执行  ./run.ps1
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot\backend

if (-not (Test-Path ".venv")) {
    Write-Host "[1/3] 创建虚拟环境..." -ForegroundColor Cyan
    python -m venv .venv
}
. .\.venv\Scripts\Activate.ps1

Write-Host "[2/3] 安装最小依赖..." -ForegroundColor Cyan
pip install -q -r requirements-min.txt

Write-Host "[3/3] 启动服务 http://localhost:8000  (Ctrl+C 退出)" -ForegroundColor Green
$env:ASR_PROVIDER = "mock"
$env:LLM_PROVIDER = "mock"
$env:ENABLE_SPEAKER = "false"   # 阶段A未装声纹依赖，先关闭
uvicorn main:app --host 0.0.0.0 --port 8000
