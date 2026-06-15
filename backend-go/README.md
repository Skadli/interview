# interview-backend — AI 实时面试辅助工具（Go 实时后端核心）

手机 PWA 拾音 → 16kHz/单声道/Int16 小端 PCM 经 WebSocket 流式发给本服务 →
转发火山引擎大模型流式 ASR → 句子端点(definite)且判定为"提问"时 → 调豆包(方舟)流式生成参考回答 → 流式回传。
说话人(本人/面试官)由外部 Python 声纹 sidecar 通过 HTTP 判定。

核心诉求：低延迟、高并发。每个 WebSocket 连接一个 `Session`，全程持有一条火山 ASR 连接（连续流）。

## 目录结构

```
backend-go/
├─ go.mod / go.sum        module=interview-backend, 依赖 gorilla/websocket
├─ util.go                字节/gzip/uuid/PCM/RMS 工具（复用自 p0）
├─ config.go             Config + 所有环境变量（复用自 p0）
├─ prompts.go            两种模式提示词 + buildPrompt(注入简历/公司文本)
├─ speaker.go            Speaker 接口 + Noop + Sidecar(带 X-Session-Id)
├─ asr.go                ASRStream 接口 + MockASR(能量VAD) + VolcASR(火山二进制协议)
├─ llm.go                LLM 接口 + MockLLM + ArkLLM(豆包 OpenAI 兼容 SSE)
├─ session.go            编排：环形缓冲 + ASR 事件循环 + 声纹 + LLM + 延迟打点
├─ main.go               config 加载 + HTTP mux(/ws /health /) + 静态前端
├─ run.ps1               启动脚本（mock / -Real 两种）
├─ selftest_test.go      自测：合成 PCM 跑通 transcript→question→answer_delta→answer_done
└─ README.md
```

## 快速启动

### Mock 模式（默认，免 key，本机跑通）

```powershell
cd backend-go
.\run.ps1
```

或手动：

```powershell
go env -w GOPROXY=https://goproxy.cn,direct
go mod tidy
$env:ASR_PROVIDER="mock"; $env:LLM_PROVIDER="mock"
go run .
```

浏览器/手机连 `http://<本机IP>:8000`，WebSocket 路径 `/ws`。

### 真实火山 ASR + 豆包 LLM

```powershell
$env:VOLC_APP_KEY="..."          # 火山语音 APP KEY
$env:VOLC_ACCESS_KEY="..."       # 火山语音 ACCESS KEY
$env:ARK_API_KEY="..."           # 方舟(豆包) API KEY
$env:ASR_PROVIDER="volc"
$env:LLM_PROVIDER="ark"
# 可选：声纹 sidecar
$env:SPEAKER_SIDECAR_URL="http://127.0.0.1:8101"
go run .
```

或 `.\run.ps1 -Real`（脚本里预留了 key 占位符）。

## 环境变量

| 变量 | 默认 | 说明 |
|---|---|---|
| `ADDR` | `:8000` | 监听地址 |
| `FRONTEND_DIR` | `../web/dist` | 静态前端目录；找不到 index.html 时 `/` 返回提示文字 |
| `ASR_PROVIDER` | `mock` | `mock` / `volc` |
| `LLM_PROVIDER` | `mock` | `mock` / `ark` |
| `VOLC_APP_KEY` | — | 火山语音 APP KEY（volc 必填） |
| `VOLC_ACCESS_KEY` | — | 火山语音 ACCESS KEY（volc 必填） |
| `VOLC_RESOURCE_ID` | `volc.bigasr.sauc.duration` | 火山资源 ID |
| `VOLC_ASR_URL` | `wss://openspeech.bytedance.com/api/v3/sauc/bigmodel` | 火山 ASR WS 地址 |
| `ARK_API_KEY` | — | 方舟 API KEY（ark 必填） |
| `ARK_BASE_URL` | `https://ark.cn-beijing.volces.com/api/v3` | 方舟基址 |
| `MODEL_FAST` | `doubao-seed-1.6-flash` | 口语化模式用（极速） |
| `MODEL_STRONG` | `doubao-seed-1.6` | 结构化模式用（重质量） |
| `SPEAKER_SIDECAR_URL` | — | 声纹服务基址；为空则 Noop（全部判面试官，无声纹也能跑通） |
| `SPEAKER_THRESH` | `0.75` | 声纹阈值（信息性，实际判定由 sidecar 返回 is_user） |
| `ENDPOINT_MS` | `650` | mock 能量VAD 句尾静音；火山模式仅作 timing 信息性字段 |

## WebSocket 协议（路径 `/ws`）

客户端 → 服务端：
- 二进制帧：Int16 PCM，单声道，16000Hz，小端。
- 文本 JSON 控制：
  - `{"type":"set_mode","mode":"conversation"|"structured"}`
  - `{"type":"enroll_start"}`
  - `{"type":"set_context","resume_text":"...","company_text":"..."}`
  - `{"type":"regenerate"}`

服务端 → 客户端 JSON：
- `{"type":"status","state":"..."}`
- `{"type":"partial","text":"..."}` — 实时中间识别结果
- `{"type":"transcript","text":"...","speaker":"user"|"interviewer","similarity":0.83}`
- `{"type":"question","text":"..."}`
- `{"type":"answer_delta","text":"..."}`
- `{"type":"answer_done","timing":{...}}`
- `{"type":"enrolled","ok":true}`

`timing` 字段：`endpoint_ms`(信息性,上游端点等待), `speaker_ms`(声纹耗时), `asr_ms`(火山流式为0),
`llm_ttft_ms`(声纹后→首字), `to_first_word_ms`(说完→首字), `perceived_first_word_ms`(=to_first_word, 火山端点已在上游),
`llm_total_ms`(首字→结束)。单位毫秒，保留 3 位小数。

## 声纹 sidecar 契约（Go → Python，base URL = `SPEAKER_SIDECAR_URL`）

- `POST /enroll`  头 `X-Session-Id:<sid>`，Body=原始 Int16 PCM 小端 16k 单声道 → `{"ok":true}`
- `POST /verify`  头 `X-Session-Id:<sid>`，Body 同上 → `{"is_user":false,"similarity":0.42}`
- `GET /health`

每个 Session 生成一个 uuid 作为 sid。`SPEAKER_SIDECAR_URL` 为空时用 NoopSpeaker（一律判面试官）。

## 编排逻辑（session.go）

- 收二进制音频：写入 30s 环形缓冲（记录基准样本号，可按 ms 切片）+ 转发 `ASRStream.SendAudio`。
- 后台 goroutine 消费 `ASRStream.Events()`：
  - `partial` → 发 `partial` 事件。
  - `final` → 起 goroutine：记 `tEnd`；按 `[start_time,end_time]` 切出该句音频；
    - `enrolling` → `Speaker.Enroll` → 发 `enrolled` → 清标志返回；
    - 否则若声纹启用 → `Verify` 得 `(isUser,sim)`，记 `tSpk`；发 `transcript`；
    - `isUser` → 发 status("你在回答") 返回；
    - 非提问(`isQuestion`) → 发 status("非提问") 返回；
    - 否则发 `question`，按 mode 选模型，`LLM.Stream` 流式发 `answer_delta`，结束发 `answer_done`。
- `set_context` 注入的简历/公司文本由 `buildPrompt` 作为"参考资料"拼进 system（两种模式都拼）。

## 验证结果（本机实测）

```
go env -w GOPROXY=https://goproxy.cn,direct   # OK
go mod tidy                                    # OK（下载 gorilla/websocket v1.5.3）
go build ./...                                 # OK
go vet ./...                                   # OK
go test ./...                                  # ok  interview-backend  ~2.1s
```

`TestPipelineMock` 喂入合成 PCM（1.2s 正弦波 + 1.0s 静音），实测产出顺序：
`status(ready) → status(mode:conversation) → transcript(interviewer) → question → answer_delta(201字) → answer_done`，
timing 全字段齐全。另含 `TestBuildPromptContext`（简历/公司文本注入）与 `TestRingBufferSlice`（毫秒切片）。

## 注意事项 / 未验证点

- **`go test -race` 不可用**：本环境无 cgo（`-race requires cgo`，缺 C 编译器）。已用普通 `go test` 通过；
  并发路径（环形缓冲、ws 写、LLM 串行）均加锁，但未经 race detector 验证。如需，请在装有 gcc/clang 的机器上
  `set CGO_ENABLED=1 && go test -race ./...`。
- **真实火山 ASR 未联调**：无 `VOLC_APP_KEY/ACCESS_KEY`，VolcASR 的二进制协议（握手头、4字节帧头、序列号、
  gzip payload、definite 解析）按方案给定的协议实现并能编译通过，但未对真实端点做过握手/识别验证。
  首次接真实 key 时建议打开 `[volc-asr]` 日志确认握手与帧解析。
- **真实豆包 LLM 未联调**：无 `ARK_API_KEY`，ArkLLM 的 SSE 解析（`data:` 行、`[DONE]`、`thinking:disabled`）
  按 OpenAI 兼容格式实现并编译通过，未对真实端点验证。
- 前端（`web/dist`）不在本目录范围内；缺失时 `/` 返回提示文字，`/ws` 正常工作。
- Windows 控制台为 GBK，日志与测试输出均未使用 emoji，避免编码崩溃。
```
