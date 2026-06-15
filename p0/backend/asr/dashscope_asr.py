"""DashScope（阿里 Paraformer 实时识别）适配器——对一段 PCM 做识别。

注意：DashScope SDK 接口可能随版本变化，接入真实 key 后请按当前 SDK 文档核对
（Recognition / RecognitionCallback / send_audio_frame / get_sentence 字段）。
P0 默认走 mock，本文件在 ASR_PROVIDER=dashscope 时才被导入。
"""
import asyncio
import numpy as np
from .base import ASR
from config import config


class DashScopeASR(ASR):
    def __init__(self):
        import dashscope
        if not config.DASHSCOPE_API_KEY:
            raise RuntimeError("缺少 DASHSCOPE_API_KEY")
        dashscope.api_key = config.DASHSCOPE_API_KEY

    async def transcribe(self, pcm_i16: np.ndarray, sample_rate: int = 16000) -> str:
        return await asyncio.to_thread(self._sync, pcm_i16, sample_rate)

    def _sync(self, pcm_i16: np.ndarray, sample_rate: int) -> str:
        from dashscope.audio.asr import Recognition, RecognitionCallback

        sentences = []

        class CB(RecognitionCallback):
            def on_event(self, result):
                try:
                    s = result.get_sentence()
                    if s and s.get("text"):
                        sentences.append(s["text"])
                except Exception:
                    pass

        rec = Recognition(
            model=config.ASR_MODEL,
            format="pcm",
            sample_rate=sample_rate,
            callback=CB(),
        )
        rec.start()
        data = pcm_i16.astype("<i2").tobytes()
        step = (sample_rate // 10) * 2  # 100ms 一帧
        for off in range(0, len(data), step):
            rec.send_audio_frame(data[off:off + step])
        rec.stop()
        return sentences[-1] if sentences else ""
