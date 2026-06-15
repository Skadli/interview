"""离线自测：合成音频喂进 Session，验证 VAD切分→ASR→提问判定→LLM流式 整条链路出事件。
不需要麦克风/网络/key。运行：python selftest.py"""
import asyncio
import numpy as np
from pipeline import Session
from vad import VadSegmenter
from speaker import SpeakerVerifier
from asr.mock import MockASR
from llm.mock import MockLLM


def make_pcm():
    sr = 16000
    t = np.arange(int(sr * 1.0)) / sr
    speech = (0.3 * np.sin(2 * np.pi * 220 * t)).astype(np.float32)   # 1s 语音
    silence = np.zeros(int(sr * 0.8), dtype=np.float32)               # 0.8s 静音收尾
    pcm = np.concatenate([speech, silence])
    return (np.clip(pcm, -1, 1) * 32767).astype(np.int16)


async def main():
    events = []

    async def send(obj):
        events.append(obj)
        if obj["type"] == "answer_delta":
            return  # 太多，不打印
        print("EVENT:", obj)

    sess = Session(send, VadSegmenter(), SpeakerVerifier(), MockASR(), MockLLM())
    pcm = make_pcm()
    for off in range(0, len(pcm), 512):
        await sess.on_audio(pcm[off:off + 512])
    await asyncio.sleep(2.0)  # 等待 _handle_segment 后台任务完成

    types = [e["type"] for e in events]
    assert "transcript" in types, "未产生 transcript（VAD未切出段？）"
    assert "question" in types, "未触发 question"
    assert "answer_delta" in types, "未产生流式回答"
    assert "answer_done" in types, "未收到 answer_done"
    answer = "".join(e["text"] for e in events if e["type"] == "answer_delta")
    timing = next(e["timing"] for e in events if e["type"] == "answer_done")
    print("\n--- 全链路通过 [PASS] ---")
    print("识别(mock):", next(e["text"] for e in events if e["type"] == "question"))
    print("回答(mock):", answer[:60], "...")
    print("延迟打点:", timing)


if __name__ == "__main__":
    asyncio.run(main())
