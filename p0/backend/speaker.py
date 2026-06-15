"""说话人验证（声纹）：开场注册用户声纹，之后每段判断"是不是用户本人"。
不是本人 = 面试官 = 触发 AI。用 resemblyzer（轻量、自带预训练模型，免 key）。
生产可换达摩院 3D-Speaker CAM++ 提升精度。

编码器较重，做成进程级单例共享；每个会话各自持有自己的 enroll 向量。
"""
import numpy as np
from config import config

_ENCODER = None
_PREPROCESS = None
_LOAD_FAILED = False


def _get_encoder():
    global _ENCODER, _PREPROCESS, _LOAD_FAILED
    if _ENCODER is None and not _LOAD_FAILED:
        try:
            from resemblyzer import VoiceEncoder, preprocess_wav
            _ENCODER = VoiceEncoder(verbose=False)
            _PREPROCESS = preprocess_wav
            print("[Speaker] resemblyzer 已加载")
        except Exception as e:  # noqa
            _LOAD_FAILED = True
            print(f"[Speaker] resemblyzer 不可用，说话人验证关闭（所有声音视为面试官）：{e}")
    return _ENCODER, _PREPROCESS


class SpeakerVerifier:
    def __init__(self):
        self._enroll_emb = None
        self._encoder = None
        self._preprocess = None
        if config.ENABLE_SPEAKER:
            self._encoder, self._preprocess = _get_encoder()

    @property
    def ready(self):
        return self._encoder is not None

    @property
    def enrolled(self):
        return self._enroll_emb is not None

    def _embed(self, wav_f32: np.ndarray):
        wav = self._preprocess(wav_f32, source_sr=config.SAMPLE_RATE)
        return self._encoder.embed_utterance(wav)  # 已 L2 归一化

    def enroll(self, wav_f32: np.ndarray) -> bool:
        if not self.ready:
            return False
        try:
            emb = self._embed(wav_f32)
        except Exception as e:  # noqa
            print(f"[Speaker] enroll 失败：{e}")
            return False
        if self._enroll_emb is None:
            self._enroll_emb = emb
        else:
            m = (self._enroll_emb + emb) / 2.0
            self._enroll_emb = m / (np.linalg.norm(m) + 1e-9)
        return True

    def verify(self, wav_f32: np.ndarray):
        """返回 (is_user, similarity)。未注册/不可用 -> (False, 0.0)。"""
        if not self.ready or self._enroll_emb is None:
            return False, 0.0
        try:
            emb = self._embed(wav_f32)
        except Exception:
            return False, 0.0
        sim = float(np.dot(emb, self._enroll_emb))
        return sim >= config.SPEAKER_THRESH, sim
