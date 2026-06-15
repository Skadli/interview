r"""声纹服务自测: 合成两段不同频率的正弦 PCM 模拟两个"说话人"。

用法(服务已在 :8101 启动后):
    .\.venv\Scripts\python.exe selftest.py

流程:
  - 对 sid=A 用频率1(220Hz)调 /enroll
  - 用频率1 调 /verify  (期望 similarity 高, is_user 倾向 True)
  - 用频率2(660Hz)调 /verify (期望 similarity 低, is_user 倾向 False)
注意: 正弦波不是真人声, 模型未必给出强区分; 这里主要验证"同源 sim 应高于异源 sim"。
"""
import sys
import json
import math
import struct
import urllib.request

BASE = "http://127.0.0.1:8101"
SR = 16000
SECS = 3


def make_pcm(freq: float) -> bytes:
    """生成 SECS 秒、采样率 16k 的正弦波 Int16 小端 PCM。"""
    n = SR * SECS
    out = bytearray()
    amp = 0.5
    for i in range(n):
        v = amp * math.sin(2 * math.pi * freq * i / SR)
        out += struct.pack("<h", int(v * 32767))
    return bytes(out)


def post(path: str, sid: str, body: bytes):
    req = urllib.request.Request(
        BASE + path, data=body, method="POST",
        headers={"X-Session-Id": sid, "Content-Type": "application/octet-stream"},
    )
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read().decode())


def get(path: str):
    with urllib.request.urlopen(BASE + path, timeout=30) as r:
        return json.loads(r.read().decode())


def main():
    print("[selftest] GET /health ->", get("/health"))
    pcm1 = make_pcm(220.0)
    pcm2 = make_pcm(660.0)

    print("[selftest] POST /enroll (sid=A, 220Hz) ->", post("/enroll", "A", pcm1))
    r_same = post("/verify", "A", pcm1)
    r_diff = post("/verify", "A", pcm2)
    print("[selftest] POST /verify (sid=A, 220Hz, same)  ->", r_same)
    print("[selftest] POST /verify (sid=A, 660Hz, diff)  ->", r_diff)

    sim_same = r_same.get("similarity", 0.0)
    sim_diff = r_diff.get("similarity", 0.0)
    print(f"[selftest] sim_same={sim_same}  sim_diff={sim_diff}  "
          f"{'OK 同源>异源' if sim_same > sim_diff else 'NOTE 未区分(降级或正弦区分弱)'}")


if __name__ == "__main__":
    sys.exit(main())
