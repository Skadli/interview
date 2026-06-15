"""DashScope（通义千问）流式适配器。把 SDK 的同步流式生成器桥接成 async 生成器。"""
import asyncio
from .base import LLM
from config import config


class DashScopeLLM(LLM):
    def __init__(self):
        import dashscope
        if not config.DASHSCOPE_API_KEY:
            raise RuntimeError("缺少 DASHSCOPE_API_KEY")
        dashscope.api_key = config.DASHSCOPE_API_KEY

    async def stream(self, system: str, user: str, model: str = ""):
        from dashscope import Generation

        model = model or config.LLM_MODEL_STRONG
        loop = asyncio.get_event_loop()
        q: asyncio.Queue = asyncio.Queue()
        DONE = object()

        def run():
            try:
                responses = Generation.call(
                    model=model,
                    messages=[
                        {"role": "system", "content": system},
                        {"role": "user", "content": user},
                    ],
                    result_format="message",
                    stream=True,
                    incremental_output=True,  # 增量输出，便于流式
                )
                for r in responses:
                    try:
                        delta = r.output.choices[0].message.content
                    except Exception:
                        delta = ""
                    if delta:
                        loop.call_soon_threadsafe(q.put_nowait, delta)
            except Exception as e:  # noqa
                loop.call_soon_threadsafe(q.put_nowait, f"[LLM错误] {e}")
            finally:
                loop.call_soon_threadsafe(q.put_nowait, DONE)

        loop.run_in_executor(None, run)
        while True:
            item = await q.get()
            if item is DONE:
                break
            yield item
