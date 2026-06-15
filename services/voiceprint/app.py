"""声纹(说话人验证) HTTP 微服务。

把 p0/backend/speaker.py 的 resemblyzer 核心算法(VoiceEncoder + preprocess_wav,
余弦相似度, L2 归一化, graceful degrade)包成一个 FastAPI 服务, 支持多会话隔离。

被 Go 核心调用。所有音频 Body 都是原始 Int16 PCM, 小端, 16000Hz, 单声道。

graceful degrade: 如果 resemblyzer 导入失败(例如 torch 在 Python 3.14 上没有 wheel),
服务仍能启动, /health 返回 ml:false, /verify 永远返回 is_user:false。
"""
import os
import time
import asyncio
import threading
from contextlib import asynccontextmanager
from collections import OrderedDict
from concurrent.futures import ThreadPoolExecutor

import numpy as np
from fastapi import FastAPI, Request, Header

# ---------------------------------------------------------------------------
# 配置(env 可覆盖)
# ---------------------------------------------------------------------------
PORT = int(os.getenv("PORT", "8101"))
HOST = os.getenv("HOST", "0.0.0.0")
SAMPLE_RATE = int(os.getenv("SAMPLE_RATE", "16000"))
SPEAKER_THRESH = float(os.getenv("SPEAKER_THRESH", "0.75"))
MAX_SESSIONS = int(os.getenv("MAX_SESSIONS", "500"))      # LRU 上限
SESSION_TTL_SEC = int(os.getenv("SESSION_TTL_SEC", "1800"))  # 30 分钟过期

# ---------------------------------------------------------------------------
# 模型加载(进程级单例, graceful degrade)
# ---------------------------------------------------------------------------
_ENCODER = None
_PREPROCESS = None
_LOAD_FAILED = False
_LOAD_ERR = ""


def _get_encoder():
    """惰性加载 resemblyzer。失败时记录原因并永久降级。"""
    global _ENCODER, _PREPROCESS, _LOAD_FAILED, _LOAD_ERR
    if _ENCODER is None and not _LOAD_FAILED:
        try:
            from resemblyzer import VoiceEncoder, preprocess_wav
            _ENCODER = VoiceEncoder(verbose=False)
            _PREPROCESS = preprocess_wav
            print("[Voiceprint] resemblyzer 已加载, 说话人验证可用")
        except Exception as e:  # noqa: BLE001
            _LOAD_FAILED = True
            _LOAD_ERR = repr(e)
            print(f"[Voiceprint] resemblyzer 不可用, 降级模式(所有声音视为面试官): {e}")
    return _ENCODER, _PREPROCESS


def _ml_ready() -> bool:
    return _ENCODER is not None


# ---------------------------------------------------------------------------
# 会话声纹存储: sid -> (embedding, last_access_ts)。带 TTL + LRU。
# ---------------------------------------------------------------------------
_sessions: "OrderedDict[str, list]" = OrderedDict()
_sessions_lock = threading.Lock()


def _evict_expired_locked():
    """调用方需持锁。删掉过期会话。"""
    now = time.time()
    dead = [sid for sid, (_, ts) in _sessions.items() if now - ts > SESSION_TTL_SEC]
    for sid in dead:
        _sessions.pop(sid, None)


def _touch_locked(sid: str):
    """调用方需持锁。把 sid 移到最近使用末尾。"""
    if sid in _sessions:
        _sessions.move_to_end(sid)


def _get_emb(sid: str):
    with _sessions_lock:
        _evict_expired_locked()
        item = _sessions.get(sid)
        if item is None:
            return None
        item[1] = time.time()
        _touch_locked(sid)
        return item[0]


def _del_emb(sid: str):
    with _sessions_lock:
        _sessions.pop(sid, None)


# ---------------------------------------------------------------------------
# 音频与推理(CPU, 放线程池)
# ---------------------------------------------------------------------------
_executor = ThreadPoolExecutor(max_workers=int(os.getenv("WORKERS", "2")))


def _pcm_to_f32(body: bytes) -> np.ndarray:
    """原始 Int16 PCM(小端) -> float32 [-1, 1)。"""
    if len(body) < 2:
        return np.zeros(0, dtype=np.float32)
    # 保证偶数字节
    if len(body) % 2 != 0:
        body = body[:-1]
    pcm = np.frombuffer(body, dtype="<i2").astype(np.float32) / 32768.0
    return pcm


def _embed(wav_f32: np.ndarray) -> np.ndarray:
    """喂模型得到 L2 归一化的 embedding。需在 _ml_ready() 为真时调用。"""
    wav = _PREPROCESS(wav_f32, source_sr=SAMPLE_RATE)
    return _ENCODER.embed_utterance(wav)  # resemblyzer 已 L2 归一化


def _enroll_sync(sid: str, wav_f32: np.ndarray) -> bool:
    if not _ml_ready():
        return False
    try:
        emb = _embed(wav_f32)
    except Exception as e:  # noqa: BLE001
        print(f"[Voiceprint] enroll 失败 sid={sid}: {e}")
        return False
    # 在同一把锁内 read-modify-write, 避免并发 enroll 互相覆盖
    with _sessions_lock:
        _evict_expired_locked()
        item = _sessions.get(sid)
        if item is None:
            new = emb
        else:
            # 多次注册取平均后重新 L2 归一化
            m = (item[0] + emb) / 2.0
            new = m / (np.linalg.norm(m) + 1e-9)
        _sessions[sid] = [new, time.time()]
        _sessions.move_to_end(sid)
        while len(_sessions) > MAX_SESSIONS:
            _sessions.popitem(last=False)
    return True


def _verify_sync(sid: str, wav_f32: np.ndarray):
    """返回 (is_user, similarity)。未注册/不可用 -> (False, 0.0)。"""
    if not _ml_ready():
        return False, 0.0
    enroll_emb = _get_emb(sid)
    if enroll_emb is None:
        return False, 0.0
    try:
        emb = _embed(wav_f32)
    except Exception:  # noqa: BLE001
        return False, 0.0
    sim = float(np.dot(emb, enroll_emb))
    return bool(sim >= SPEAKER_THRESH), sim


async def _run(fn, *args):
    loop = asyncio.get_running_loop()
    return await loop.run_in_executor(_executor, fn, *args)


# ---------------------------------------------------------------------------
# FastAPI 应用
# ---------------------------------------------------------------------------
@asynccontextmanager
async def _lifespan(_app: FastAPI):
    # 进程启动时尝试加载模型(降级也不会抛)
    _get_encoder()
    print(f"[Voiceprint] 监听 :{PORT}  ml={_ml_ready()}  thresh={SPEAKER_THRESH}")
    yield
    _executor.shutdown(wait=False)


app = FastAPI(title="Voiceprint Service", version="1.0", lifespan=_lifespan)


@app.get("/health")
async def health():
    return {"status": "ok", "ml": _ml_ready()}


@app.post("/enroll")
async def enroll(request: Request, x_session_id: str = Header(default="")):
    sid = x_session_id or "default"
    body = await request.body()
    wav = _pcm_to_f32(body)
    if wav.size == 0:
        return {"ok": False}
    ok = await _run(_enroll_sync, sid, wav)
    return {"ok": bool(ok)}


@app.post("/verify")
async def verify(request: Request, x_session_id: str = Header(default="")):
    sid = x_session_id or "default"
    body = await request.body()
    wav = _pcm_to_f32(body)
    if wav.size == 0:
        return {"is_user": False, "similarity": 0.0}
    is_user, sim = await _run(_verify_sync, sid, wav)
    return {"is_user": is_user, "similarity": round(sim, 4)}


@app.delete("/session/{sid}")
async def delete_session(sid: str):
    _del_emb(sid)
    return {"ok": True}


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host=HOST, port=PORT, log_level="info")
