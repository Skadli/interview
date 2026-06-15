"""P0 配置：全部用环境变量覆盖，便于分阶段验证。"""
import os


def _b(name, default):
    return os.getenv(name, str(default)).lower() in ("1", "true", "yes", "on")


class Config:
    HOST = os.getenv("HOST", "0.0.0.0")
    PORT = int(os.getenv("PORT", "8000"))

    # 服务选择：mock（默认，免 key 跑通管道） | dashscope（真实）
    ASR_PROVIDER = os.getenv("ASR_PROVIDER", "mock")
    LLM_PROVIDER = os.getenv("LLM_PROVIDER", "mock")

    # 本地模型开关
    ENABLE_VAD = _b("ENABLE_VAD", True)          # 关掉则用能量VAD兜底
    ENABLE_SPEAKER = _b("ENABLE_SPEAKER", True)  # 关掉则所有声音都当面试官

    # 阿里 DashScope（一个 key 同时用 ASR + LLM）
    DASHSCOPE_API_KEY = os.getenv("DASHSCOPE_API_KEY", "")
    ASR_MODEL = os.getenv("ASR_MODEL", "paraformer-realtime-v2")
    LLM_MODEL_FAST = os.getenv("LLM_MODEL_FAST", "qwen-turbo")    # 口语化用快模型
    LLM_MODEL_STRONG = os.getenv("LLM_MODEL_STRONG", "qwen-plus")  # 结构化用强模型

    # 音频 / VAD 参数
    SAMPLE_RATE = 16000
    VAD_WINDOW = 512                                  # 32ms @16k（Silero 要求 512）
    VAD_SPEECH_THRESH = float(os.getenv("VAD_SPEECH_THRESH", "0.5"))
    VAD_START_WINDOWS = int(os.getenv("VAD_START_WINDOWS", "2"))   # 连续N窗判定开始说话
    ENDPOINT_MS = int(os.getenv("ENDPOINT_MS", "650"))            # 静音多久判定"说完了"
    PREROLL_MS = int(os.getenv("PREROLL_MS", "200"))             # 句首回看，避免吞字
    MIN_SEG_MS = int(os.getenv("MIN_SEG_MS", "350"))            # 短于此丢弃（噪音/口头语）
    ENERGY_THRESH = float(os.getenv("ENERGY_THRESH", "0.015"))   # 能量VAD兜底阈值（RMS）

    # 说话人验证：余弦相似度 >= 阈值 判为"用户本人"
    SPEAKER_THRESH = float(os.getenv("SPEAKER_THRESH", "0.75"))

    # P0 先用静态简历/公司文本占位（P1 换成上传解析+公司简报）
    RESUME_TEXT = os.getenv("RESUME_TEXT", "")
    COMPANY_TEXT = os.getenv("COMPANY_TEXT", "")


config = Config()
