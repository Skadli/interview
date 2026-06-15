# 声纹服务启动脚本 (Windows / PowerShell)
#
# resemblyzer 依赖 torch。torch 在 Python 3.14 上通常没有预编译 wheel,
# 因此优先尝试用 Python 3.12 / 3.11 建 venv; 都没有再 fallback 到默认 python。
#
# 用法:
#   .\run.ps1            # 建/复用 venv, 装依赖, 启动服务
#   .\run.ps1 -NoInstall # 跳过 pip install, 直接启动(venv 已就绪时更快)

param(
    [switch]$NoInstall
)

$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $here

$venv = Join-Path $here ".venv"
$venvPy = Join-Path $venv "Scripts\python.exe"

function Find-Python {
    # 依次尝试 py -3.12, py -3.11, 再 fallback 到 python
    foreach ($ver in @("3.12", "3.11")) {
        try {
            $v = & py "-$ver" --version 2>$null
            if ($LASTEXITCODE -eq 0) {
                Write-Host "[run] 找到 Python $ver" -ForegroundColor Green
                return @("py", "-$ver")
            }
        } catch {}
    }
    Write-Host "[run] 未找到 Python 3.12/3.11, fallback 到默认 python" -ForegroundColor Yellow
    Write-Host "[run] 警告: 默认 python 若为 3.14, torch 很可能装不上, 服务将以降级模式启动" -ForegroundColor Yellow
    return @("python")
}

# 1. 建 venv (若不存在)
if (-not (Test-Path $venvPy)) {
    $pyCmd = Find-Python
    Write-Host "[run] 用 $($pyCmd -join ' ') 创建 venv ..." -ForegroundColor Cyan
    & $pyCmd[0] $pyCmd[1..($pyCmd.Length - 1)] -m venv $venv
    if ($LASTEXITCODE -ne 0) { throw "创建 venv 失败" }
}

# 2. 装依赖
if (-not $NoInstall) {
    Write-Host "[run] 升级 pip ..." -ForegroundColor Cyan
    & $venvPy -m pip install --upgrade pip
    Write-Host "[run] 安装依赖 (含 resemblyzer/torch, 可能较慢) ..." -ForegroundColor Cyan
    & $venvPy -m pip install -r (Join-Path $here "requirements.txt")
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[run] 依赖安装失败(很可能是 torch 在当前 Python 版本无 wheel)。" -ForegroundColor Red
        Write-Host "[run] 请安装 Python 3.12 或 3.11 (py install 3.12), 删除 .venv 后重跑。" -ForegroundColor Red
        Write-Host "[run] 仍尝试以降级模式启动服务 ..." -ForegroundColor Yellow
    }
}

# 3. 启动服务 (resemblyzer 导入失败也能启动, /health 返回 ml:false)
$env:PORT = if ($env:PORT) { $env:PORT } else { "8101" }
Write-Host "[run] 启动声纹服务 :$($env:PORT) ..." -ForegroundColor Green
& $venvPy (Join-Path $here "app.py")
