# interview-backend 启动脚本（Windows / PowerShell）
# 用法：
#   .\run.ps1            # 默认 mock（免 key，本机跑通）
#   .\run.ps1 -Real      # 真实火山 ASR + 豆包 LLM（需先设好环境变量/下方填 key）

param(
    [switch]$Real
)

$ErrorActionPreference = "Stop"
Set-Location -Path $PSScriptRoot

# CN 网络拉依赖
go env -w GOPROXY=https://goproxy.cn,direct | Out-Null

Write-Host "go mod tidy ..."
go mod tidy

if ($Real) {
    Write-Host "== 真实模式：火山 ASR + 豆包 LLM ==" -ForegroundColor Cyan
    # 必填（也可在外部 shell 提前 export）：
    # 火山语音鉴权（二选一）：
    #   新版统一鉴权（推荐，大模型 sauc 端点）：单个 VOLC_API_KEY -> 头 X-Api-Key
    #   旧版两段式：VOLC_APP_KEY + VOLC_ACCESS_KEY -> 头 X-Api-App-Key / X-Api-Access-Key
    # 代码里 VOLC_API_KEY 非空时优先走新版；否则回退两段式。
    if (-not $env:VOLC_API_KEY -and -not $env:VOLC_APP_KEY) {
        $env:VOLC_API_KEY = "<你的火山语音 API KEY (X-Api-Key)>"
    }
    if (-not $env:ARK_API_KEY)     { $env:ARK_API_KEY     = "<你的方舟 API KEY>" }
    $env:ASR_PROVIDER = "volc"
    $env:LLM_PROVIDER = "ark"
    # 可选：声纹 sidecar
    # $env:SPEAKER_SIDECAR_URL = "http://127.0.0.1:8101"
}
else {
    Write-Host "== Mock 模式：免 key 跑通管道 ==" -ForegroundColor Green
    $env:ASR_PROVIDER = "mock"
    $env:LLM_PROVIDER = "mock"
}

# 通用可调
if (-not $env:ADDR)         { $env:ADDR = ":8000" }
if (-not $env:FRONTEND_DIR) { $env:FRONTEND_DIR = "../web/dist" }

Write-Host "go build ..."
go build ./...

Write-Host "starting on $env:ADDR (ASR=$env:ASR_PROVIDER LLM=$env:LLM_PROVIDER) ..."
go run .
