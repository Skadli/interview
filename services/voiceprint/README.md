# 声纹(说话人验证)微服务 voiceprint

把 `p0/backend/speaker.py` 的 resemblyzer 核心算法(VoiceEncoder + preprocess_wav、
余弦相似度、L2 归一化)封装成一个 FastAPI HTTP 服务，支持多会话隔离，被 Go 实时核心(:8000)调用。

监听端口: **8101**

## 用途与判定逻辑

开场用用户本人的声音注册声纹(enroll)，之后每段语音判断"是不是用户本人"。
不是本人 = 面试官 = 触发 AI 作答。相似度 >= 阈值(默认 0.75)即判为本人。

## HTTP 契约

所有音频 Body 都是 **原始 Int16 PCM，小端，16000Hz，单声道**
(`Content-Type: application/octet-stream`)。会话用请求头 `X-Session-Id` 区分。

| 方法   | 路径             | 头                    | Body          | 返回 |
|--------|------------------|-----------------------|---------------|------|
| POST   | `/enroll`        | `X-Session-Id: <sid>` | Int16 PCM 16k | `{"ok":true}` |
| POST   | `/verify`        | `X-Session-Id: <sid>` | Int16 PCM 16k | `{"is_user":false,"similarity":0.42}` |
| DELETE | `/session/{sid}` | -                     | -             | `{"ok":true}` |
| GET    | `/health`        | -                     | -             | `{"status":"ok","ml":true}` |

- `/enroll` 可对同一 sid 多次调用，内部对 embedding 取平均并重新 L2 归一化。
- `/verify` 返回的 `similarity` 是与该 sid 注册声纹的余弦相似度(0~1)，
  `is_user` 为 `similarity >= SPEAKER_THRESH`。
- 未注册或会话不存在时 `/verify` 返回 `{"is_user":false,"similarity":0.0}`。
- `/session/{sid}` 用于会话结束时清理内存中的声纹。

## 会话隔离与内存管理

- 内存 `dict`: `sid -> (embedding, last_access)`，进程级共享编码器、按会话各持声纹向量。
- TTL: 默认 30 分钟未访问的会话自动过期(`SESSION_TTL_SEC`)。
- LRU: 最多保留 500 个会话(`MAX_SESSIONS`)，超出删最久未用的。
- CPU 推理放线程池(`run_in_executor`)，不阻塞事件循环。

## 环境变量

| 变量              | 默认    | 说明 |
|-------------------|---------|------|
| `PORT`            | `8101`  | 监听端口 |
| `HOST`            | `0.0.0.0` | 监听地址 |
| `SAMPLE_RATE`     | `16000` | 采样率 |
| `SPEAKER_THRESH`  | `0.75`  | 判为本人的余弦相似度阈值 |
| `MAX_SESSIONS`    | `500`   | LRU 会话上限 |
| `SESSION_TTL_SEC` | `1800`  | 会话过期秒数 |
| `WORKERS`         | `2`     | 推理线程池大小 |

## 降级模式(graceful degrade)

如果 `resemblyzer` / `torch` 导入失败，服务**仍能正常启动**，进入降级模式：

- `GET /health` 返回 `{"status":"ok","ml":false}`
- `POST /verify` 永远返回 `{"is_user":false,"similarity":0.0}`
- `POST /enroll` 返回 `{"ok":false}`

这样在没有 ML 依赖的机器上(例如本机 Python 3.14)整个系统仍能跑通：
所有声音都被视为面试官，AI 照常作答，只是失去"过滤用户本人声音"的能力。

## 服务器安装与运行(Python 3.11 / 3.12)

> **必须用 Python 3.11 或 3.12。** resemblyzer 依赖 torch，torch 目前没有
> Python 3.14 的预编译 wheel，在 3.14 上 `pip install resemblyzer` 会失败(进而降级)。

### Linux / macOS 服务器

```bash
cd services/voiceprint
python3.12 -m venv .venv          # 或 python3.11
. .venv/bin/activate
pip install --upgrade pip
pip install -r requirements.txt   # 会带入 torch, 较慢
uvicorn app:app --host 0.0.0.0 --port 8101
```

### Windows 服务器

```powershell
cd services\voiceprint
.\run.ps1            # 自动找 py -3.12 / -3.11 建 venv、装依赖、启动
.\run.ps1 -NoInstall # venv 已就绪时跳过 pip install 直接启动
```

启动后验证:

```bash
curl http://127.0.0.1:8101/health     # 期望 {"status":"ok","ml":true}
```

ml 为 true 才说明声纹真正可用; 若为 false 请检查 torch/resemblyzer 是否装上
(很可能是 Python 版本不对)。

## 为什么本机不装 torch

本机是 Windows 10 + Python 3.14，**torch 没有 3.14 的 wheel**，强装只会卡死/失败。
所以本机只做：

1. 语法检查 `app.py`。
2. 只装 `fastapi + uvicorn + numpy`(不装 resemblyzer) 启动服务，验证降级模式可用
   (`/health` 返回 `ml:false`、`/verify` 返回 `is_user:false`)。

真实的声纹区分能力(同源相似度高、异源相似度低)必须在装好 torch 的
服务器(Python 3.11/3.12)上用 `selftest.py` 验证。

## 自测

服务在 :8101 启动后:

```bash
python selftest.py
```

会合成两段不同频率的正弦 PCM 模拟两个"说话人"，验证同源相似度高于异源。
注意正弦波不是真人声，区分度有限，主要用于验证链路通畅。
