"""WS 端到端测试：连真实运行的服务，发合成PCM二进制帧，校验事件流。"""
import asyncio, json
import numpy as np
import websockets


def make_pcm():
    sr = 16000
    t = np.arange(int(sr * 1.0)) / sr
    speech = (0.3 * np.sin(2 * np.pi * 220 * t)).astype(np.float32)
    silence = np.zeros(int(sr * 0.8), dtype=np.float32)
    pcm = np.concatenate([speech, silence])
    return (np.clip(pcm, -1, 1) * 32767).astype(np.int16)


async def main():
    got = []
    async with websockets.connect("ws://127.0.0.1:8000/ws", max_size=None) as ws:
        await ws.send(json.dumps({"type": "set_mode", "mode": "structured"}))
        pcm = make_pcm()
        for off in range(0, len(pcm), 512):
            await ws.send(pcm[off:off + 512].tobytes())
            await asyncio.sleep(0.005)
        # 收事件直到 answer_done
        try:
            while True:
                msg = json.loads(await asyncio.wait_for(ws.recv(), timeout=5))
                got.append(msg)
                if msg["type"] != "answer_delta":
                    print("RECV:", msg)
                if msg["type"] == "answer_done":
                    break
        except asyncio.TimeoutError:
            print("超时")
    types = {m["type"] for m in got}
    ans = "".join(m["text"] for m in got if m["type"] == "answer_delta")
    assert {"transcript", "question", "answer_delta", "answer_done"} <= types, f"缺事件: {types}"
    print("\n[WS PASS] 结构化回答片段:", ans[:50], "...")


asyncio.run(main())
