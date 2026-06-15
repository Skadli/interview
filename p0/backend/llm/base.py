from abc import ABC, abstractmethod
from typing import AsyncIterator


class LLM(ABC):
    @abstractmethod
    def stream(self, system: str, user: str, model: str = "") -> AsyncIterator[str]:
        """流式返回回答增量（异步生成器，逐 token/逐片 yield）。"""
        ...
