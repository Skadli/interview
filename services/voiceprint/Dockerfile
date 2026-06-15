# syntax=docker/dockerfile:1
# 声纹服务：Python 3.12（torch 有 3.12 轮子）。CPU 版 torch，无需 GPU。
# 构建上下文为项目根目录（见 docker-compose.yml: context: .）
FROM python:3.12-slim

WORKDIR /app

# 运行时依赖：libsndfile1(librosa/soundfile)、curl(健康检查)
# 构建期依赖：build-essential —— 编译 webrtcvad(resemblyzer 的 C 扩展依赖，无预编译轮子)，装完即清理
RUN apt-get update && apt-get install -y --no-install-recommends \
      libsndfile1 curl build-essential \
 && rm -rf /var/lib/apt/lists/*

COPY services/voiceprint/requirements.txt ./
# 先装 CPU 版 torch（不拉 CUDA，体积小很多），再装其余依赖；最后清理编译器以瘦身
RUN pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu \
 && pip install --no-cache-dir -r requirements.txt \
 && apt-get purge -y --auto-remove build-essential \
 && rm -rf /root/.cache

COPY services/voiceprint/ ./

ENV PORT=8101 HOST=0.0.0.0 SPEAKER_THRESH=0.75
EXPOSE 8101
# torch + 模型加载较慢，给足 start-period
HEALTHCHECK --interval=15s --timeout=3s --retries=10 --start-period=180s \
  CMD curl -fsS http://localhost:8101/health || exit 1
CMD ["sh","-c","uvicorn app:app --host ${HOST:-0.0.0.0} --port ${PORT:-8101}"]
