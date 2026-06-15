"""Mock LLM：不需要 key，按模式吐出模板答案并逐字流式输出，用于跑通链路+测延迟。"""
import asyncio
from .base import LLM


class MockLLM(LLM):
    async def stream(self, system: str, user: str, model: str = ""):
        await asyncio.sleep(0.2)  # 模拟 TTFT（首字延迟）
        snippet = user[:20]
        if "结构化" in system or "公考" in system:
            text = (
                "【思路】此题属综合分析，按“表态—分析—对策”框架作答。\n"
                f"一是表明态度：对于“{snippet}”，应理性看待、一分为二。\n"
                "二是分析原因与影响：从个人选择、社会环境两个层面展开。\n"
                "三是落实对策：结合岗位职责，提出可操作的引导与保障措施。\n"
                "（以上为 mock 模拟回答，接入真实模型后自动替换）"
            )
        else:
            text = (
                f"核心回答：关于“{snippet}”，我先给结论——"
                "结合我过往的项目经历，我认为关键在于……（此处展开 1-2 句理由）。"
                "（以上为 mock 模拟回答，接入真实模型后自动替换）"
            )
        for ch in text:
            await asyncio.sleep(0.012)  # 模拟逐字流式
            yield ch
