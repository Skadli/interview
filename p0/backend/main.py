"""P0 服务入口：FastAPI + WebSocket，托管前端静态页。
运行：在 backend 目录下 `uvicorn main:app --host 0.0.0.0 --port 8000`
"""
import json
from pathlib import Path

import numpy as np
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles

from config import config
from vad import VadSegmenter
from speaker import SpeakerVerifier
from pipeline import Session

FRONT = Path(__file__).resolve().parent.parent / "frontend"
app = FastAPI(title="AI面试助手 P0")


def make_asr():
    if config.ASR_PROVIDER == "dashscope":
        from asr.dashscope_asr import DashScopeASR
        return DashScopeASR()
    from asr.mock import MockASR
    return MockASR()


def make_llm():
    if config.LLM_PROVIDER == "dashscope":
        from llm.dashscope_llm import DashScopeLLM
        return DashScopeLLM()
    from llm.mock import MockLLM
    return MockLLM()


@app.on_event("startup")
async def _boot():
    print(f"[boot] ASR={config.ASR_PROVIDER} LLM={config.LLM_PROVIDER} "
          f"VAD={config.ENABLE_VAD} SPEAKER={config.ENABLE_SPEAKER} "
          f"endpoint={config.ENDPOINT_MS}ms thresh={config.SPEAKER_THRESH}")
    # 预热共享编码器（首个会话不再卡顿）
    if config.ENABLE_SPEAKER:
        SpeakerVerifier()


@app.get("/")
async def index():
    return FileResponse(str(FRONT / "index.html"))


app.mount("/static", StaticFiles(directory=str(FRONT)), name="static")


@app.websocket("/ws")
async def ws_endpoint(ws: WebSocket):
    await ws.accept()

    async def send(obj):
        await ws.send_text(json.dumps(obj, ensure_ascii=False))

    sess = Session(send, VadSegmenter(), SpeakerVerifier(), make_asr(), make_llm())
    await send({"type": "status", "state": "已连接"})
    try:
        while True:
            msg = await ws.receive()
            if msg.get("type") == "websocket.disconnect":
                break
            b = msg.get("bytes")
            t = msg.get("text")
            if b is not None:
                await sess.on_audio(np.frombuffer(b, dtype=np.int16))
            elif t is not None:
                await sess.on_control(json.loads(t))
    except WebSocketDisconnect:
        pass
    except Exception as e:  # noqa
        print(f"[ws] 异常：{e}")
