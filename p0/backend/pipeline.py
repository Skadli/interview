"""单会话编排：音频 -> VAD切分 -> 声纹判定 -> ASR -> 是否提问 -> LLM流式 -> 事件下发。
内置端到端延迟打点。"""
import asyncio
import time
import numpy as np
from config import config
from prompts import build


def now_ms():
    return time.perf_counter() * 1000.0


class Session:
    def __init__(self, send, vad, speaker, asr, llm):
        self._send = send                 # async fn(dict)
        self._lock = asyncio.Lock()       # 串行化下发
        self.vad = vad
        self.speaker = speaker
        self.asr = asr
        self.llm = llm
        self.mode = "conversation"        # conversation | structured
        self.enrolling = False

    async def send(self, obj):
        async with self._lock:
            await self._send(obj)

    async def on_control(self, msg: dict):
        t = msg.get("type")
        if t == "set_mode":
            self.mode = msg.get("mode", "conversation")
            await self.send({"type": "status", "state": f"已切换模式：{self.mode}"})
        elif t == "enroll_start":
            self.enrolling = True
            await self.send({"type": "status", "state": "声纹注册中：请正常说一句话（约5秒）…"})

    async def on_audio(self, pcm_i16: np.ndarray):
        f32 = pcm_i16.astype(np.float32) / 32768.0
        for ev, seg in self.vad.push(f32):
            if ev == "speech_start":
                await self.send({"type": "status", "state": "检测到说话…"})
            elif ev == "speech_end":
                # 异步处理，避免阻塞音频接收
                asyncio.create_task(self._handle_segment(seg))

    async def _handle_segment(self, seg_f32: np.ndarray):
        # t_end：VAD 判定"说完了"的时刻（已包含端点静音等待）
        t_end = now_ms()

        if self.enrolling:
            ok = await asyncio.to_thread(self.speaker.enroll, seg_f32)
            self.enrolling = False
            await self.send({"type": "enrolled", "ok": ok})
            await self.send({"type": "status",
                             "state": "声纹注册完成，可开始监听" if ok else "声纹模块不可用"})
            return

        # 说话人判定（与后续 ASR 串行，但耗时很短）
        is_user, sim = False, 0.0
        if self.speaker.ready and self.speaker.enrolled:
            is_user, sim = await asyncio.to_thread(self.speaker.verify, seg_f32)
        t_spk = now_ms()
        speaker_label = "user" if is_user else "interviewer"

        # ASR
        pcm_i16 = (np.clip(seg_f32, -1.0, 1.0) * 32767).astype(np.int16)
        text = await self.asr.transcribe(pcm_i16, config.SAMPLE_RATE)
        t_asr = now_ms()
        await self.send({"type": "transcript", "text": text,
                         "speaker": speaker_label, "similarity": round(sim, 3)})

        if is_user:
            await self.send({"type": "status", "state": "检测到你在回答（不触发AI）"})
            return
        if not self._is_question(text):
            await self.send({"type": "status", "state": "面试官发言（判定为非提问，跳过）"})
            return

        # 触发 LLM 流式回答
        await self.send({"type": "question", "text": text})
        system, user = build(self.mode, text)
        model = config.LLM_MODEL_FAST if self.mode == "conversation" else config.LLM_MODEL_STRONG

        t_first = None
        async for delta in self.llm.stream(system, user, model):
            if t_first is None:
                t_first = now_ms()
            await self.send({"type": "answer_delta", "text": delta})
        t_done = now_ms()

        to_first = round(t_first - t_end, 1) if t_first else None
        timing = {
            "endpoint_ms": config.ENDPOINT_MS,          # 固定端点静音等待
            "speaker_ms": round(t_spk - t_end, 1),       # 声纹判定
            "asr_ms": round(t_asr - t_spk, 1),           # 识别
            "llm_ttft_ms": round(t_first - t_asr, 1) if t_first else None,  # 模型首字
            "to_first_word_ms": to_first,                # 说完→首字（不含端点静音）
            # 感知延迟 ≈ 端点静音 + 上面这段
            "perceived_first_word_ms": round(config.ENDPOINT_MS + to_first, 1) if to_first else None,
            "llm_total_ms": round(t_done - (t_first or t_done), 1),
        }
        await self.send({"type": "answer_done", "timing": timing})

    @staticmethod
    def _is_question(text: str) -> bool:
        if not text or len(text.strip()) < 4:
            return False
        kw = ["?", "？", "吗", "呢", "为什么", "如何", "怎么", "怎样", "谈谈", "说说",
              "介绍", "请", "什么", "是否", "看法", "评价", "如果", "为何", "聊聊"]
        return any(k in text for k in kw)
