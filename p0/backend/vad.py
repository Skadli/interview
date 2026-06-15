"""流式 VAD 切分器：push float32 PCM(16k) -> 产生 speech_start / speech_end(segment) 事件。
优先 Silero VAD；不可用时回退能量阈值（仅需 numpy，便于轻量验证）。

注意：Silero 模型是有状态的（内部 RNN），因此每个会话各自持有一个实例，
并在每段结束后 reset_states()，避免跨段/跨会话状态串扰。
"""
import numpy as np
from config import config


class VadSegmenter:
    def __init__(self):
        self.sr = config.SAMPLE_RATE
        self.win = config.VAD_WINDOW
        win_ms = self.win * 1000 // self.sr  # 每窗毫秒数（32）
        self.thresh = config.VAD_SPEECH_THRESH
        self.start_need = max(1, config.VAD_START_WINDOWS)
        self.end_need = max(1, config.ENDPOINT_MS // win_ms)
        self.preroll_need = max(0, config.PREROLL_MS // win_ms)
        self.min_seg_samples = config.MIN_SEG_MS * self.sr // 1000

        self._buf = np.zeros(0, dtype=np.float32)
        self._in_speech = False
        self._speech_run = 0
        self._silence_run = 0
        self._seg = []
        self._preroll = []

        self._model = None
        self._torch = None
        self._use_silero = False
        if config.ENABLE_VAD:
            self._init_silero()

    def _init_silero(self):
        try:
            import torch
            from silero_vad import load_silero_vad
            self._model = load_silero_vad()
            self._torch = torch
            self._use_silero = True
            print("[VAD] 使用 Silero VAD")
        except Exception as e:  # noqa
            print(f"[VAD] Silero 不可用，回退能量VAD：{e}")

    def _speech_prob(self, window: np.ndarray) -> float:
        if self._use_silero:
            t = self._torch.from_numpy(window)
            with self._torch.no_grad():
                return float(self._model(t, self.sr).item())
        rms = float(np.sqrt(np.mean(window ** 2)) + 1e-9)
        return 1.0 if rms > config.ENERGY_THRESH else 0.0

    def _reset_model(self):
        if self._use_silero:
            try:
                self._model.reset_states()
            except Exception:
                pass

    def push(self, pcm_f32: np.ndarray):
        """返回事件列表：("speech_start", None) / ("speech_end", segment_float32)"""
        events = []
        self._buf = np.concatenate([self._buf, pcm_f32])
        while len(self._buf) >= self.win:
            window = self._buf[:self.win].copy()
            self._buf = self._buf[self.win:]
            is_speech = self._speech_prob(window) >= self.thresh

            if not self._in_speech:
                self._preroll.append(window)
                if len(self._preroll) > self.preroll_need:
                    self._preroll.pop(0)
                if is_speech:
                    self._speech_run += 1
                    if self._speech_run >= self.start_need:
                        self._in_speech = True
                        self._silence_run = 0
                        self._seg = list(self._preroll)
                        self._preroll = []
                        events.append(("speech_start", None))
                else:
                    self._speech_run = 0
            else:
                self._seg.append(window)
                if is_speech:
                    self._silence_run = 0
                else:
                    self._silence_run += 1
                    if self._silence_run >= self.end_need:
                        seg = np.concatenate(self._seg) if self._seg else np.zeros(0, np.float32)
                        self._in_speech = False
                        self._speech_run = 0
                        self._silence_run = 0
                        self._seg = []
                        self._reset_model()
                        if len(seg) >= self.min_seg_samples:
                            events.append(("speech_end", seg))
        return events
