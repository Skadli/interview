"""Mock ASR：不需要 key，按顺序吐出示例面试问题，让整条链路（含 LLM）能跑通、能测延迟。"""
import asyncio
import numpy as np
from .base import ASR

SAMPLE_QUESTIONS = [
    "请你先做一个简单的自我介绍。",
    "谈谈你最有成就感的一个项目，遇到了什么困难，又是怎么解决的？",
    "你为什么想加入我们公司？",
    "如果团队里的同事不太配合你的工作，你会怎么处理？",
    "对于'年轻人该不该躺平'这个社会现象，谈谈你的看法。",
    "领导临时让你在一周内组织一场两百人的招聘宣讲会，你会怎么安排？",
]


class MockASR(ASR):
    def __init__(self):
        self._i = 0

    async def transcribe(self, pcm_i16: np.ndarray, sample_rate: int = 16000) -> str:
        await asyncio.sleep(0.05)  # 模拟少量识别耗时
        q = SAMPLE_QUESTIONS[self._i % len(SAMPLE_QUESTIONS)]
        self._i += 1
        return q
