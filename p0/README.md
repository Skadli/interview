# P0 验证台 — AI 辅助面试主链路

目标：把 **拾音 → VAD切分 → 声纹区分用户/面试官 → 触发 → ASR → LLM流式 → 手机显示** 跑通，
**实测两件事**：① 说话人分离准不准；② 端到端延迟多少。

```
p0/
├─ backend/        FastAPI + WebSocket 编排（VAD/声纹/ASR/LLM 适配器，含延迟打点）
└─ frontend/       手机网页：麦克风采集→16k PCM→WS；实时字幕+流式答案+延迟显示
```

## 分三阶段验证（强烈建议按顺序）

### 阶段 A · 跑通管道（最小依赖，免 key，电脑 localhost）
验证 WebSocket、音频采集、VAD 切分、流式显示、延迟测量框架是否通。
```powershell
cd p0
./run.ps1        # 自动建 venv、装最小依赖、以 mock 启动
```
浏览器开 `http://localhost:8000` → ① 连接 → ③ 开始监听 → 对着麦克风说一句话。
预期：你一停顿，"面试官问题"区出现一条 mock 示例问题，"AI 参考回答"逐字流式蹦出，底部显示延迟分解。
> 阶段A 关闭了声纹（`ENABLE_SPEAKER=false`），用能量VAD，所以任何说话都会触发——这是正常的，只为验证管道。

### 阶段 B · 真实 VAD + 声纹分离（验证"分离准不准"）
装重依赖（含 torch，首次较慢、会下载小模型）：
```powershell
cd p0\backend
. .\.venv\Scripts\Activate.ps1
pip install silero-vad resemblyzer
$env:ASR_PROVIDER="mock"; $env:LLM_PROVIDER="mock"
$env:ENABLE_SPEAKER="true"; $env:ENABLE_VAD="true"
uvicorn main:app --host 0.0.0.0 --port 8000
```
验证步骤：① 连接 → ② 声纹注册（你本人念一句）→ ③ 开始监听。
- 你自己说话：transcript 显示 `[你 · sim=0.8x]`，状态"检测到你在回答（不触发AI）"。
- 放一段别人的声音 / 让朋友说：显示 `[面试官 · sim=0.5x]` 并触发回答。
- **看 sim 值**：本人通常 > 0.75，他人 < 0.7。据此调 `SPEAKER_THRESH`（默认 0.75）。

### 阶段 C · 真实 ASR + LLM（验证"端到端真实延迟"）
```powershell
$env:DASHSCOPE_API_KEY="sk-你的key"
$env:ASR_PROVIDER="dashscope"; $env:LLM_PROVIDER="dashscope"
uvicorn main:app --host 0.0.0.0 --port 8000
```
（DashScope 一个 key 同时用 Paraformer 实时识别 + Qwen。换火山/讯飞只需照 `asr/llm` 目录新增一个适配器。）

### 上手机测真实场景
手机用麦克风需 HTTPS：
```powershell
python make_cert.py     # 生成自签证书（含本机局域网IP）
uvicorn main:app --host 0.0.0.0 --port 8443 --ssl-keyfile key.pem --ssl-certfile cert.pem
```
手机连同一 WiFi，浏览器开 `https://<电脑局域网IP>:8443`，信任证书后操作。
**真实场景**：电脑放面试官声音（外放），手机放桌上注册你声纹后监听。
> 也可用 `cloudflared`/`ngrok` 隧道获得免信任的 HTTPS 公网地址。

## 怎么读延迟（底部那行）
- `perceived_first_word_ms`：**感知首字延迟** ≈ 端点静音 + 说完后处理，这是用户真正感受到的"反应速度"。
- `endpoint_ms`：固定静音等待（默认 650ms，是延迟大头）。想更快就调小 `ENDPOINT_MS`，但太小会把话截断。
- `to_first_word_ms`：说完→首字（不含端点静音）= 声纹 + ASR + 模型首字。
- `speaker_ms / asr_ms / llm_ttft_ms`：各环节耗时，定位瓶颈。

## 关键可调参数（环境变量）
| 变量 | 默认 | 作用 |
|---|---|---|
| `ENDPOINT_MS` | 650 | 判定"说完了"的静音时长；**延迟与截断的权衡** |
| `SPEAKER_THRESH` | 0.75 | 声纹判定阈值；按实测 sim 调 |
| `VAD_SPEECH_THRESH` | 0.5 | Silero 语音概率阈值 |
| `MIN_SEG_MS` | 350 | 短于此的段当噪音丢弃 |
| `ASR_PROVIDER`/`LLM_PROVIDER` | mock | mock / dashscope |
| `ENABLE_VAD`/`ENABLE_SPEAKER` | true | 关掉走能量VAD / 关掉不分离 |

## P0 结论应回答
1. 安静环境下，本人 vs 他人声纹 sim 差距够不够拉开（能否稳定分离）？外放+环境噪音下衰减多少？
2. `perceived_first_word_ms` 真实值落在多少？哪个环节是瓶颈（多半是 endpoint + ASR）？
3. 据此决定：是否继续走"手机拾音"路线，还是上 P4"电脑端双路音轨"。
