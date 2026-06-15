from abc import ABC, abstractmethod
import numpy as np


class ASR(ABC):
    @abstractmethod
    async def transcribe(self, pcm_i16: np.ndarray, sample_rate: int = 16000) -> str:
        """对一段完整 PCM(int16) 做识别，返回文本。"""
        ...
