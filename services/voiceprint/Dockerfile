# syntax=docker/dockerfile:1
# 声纹服务：Python 3.12（torch 有 3.12 轮子）。CPU 版 torch，无需 GPU。
# 构建上下文为项目根目录（见 docker-compose.yml: context: .）
FROM python:3.12-slim

WORKDIR /app

# resemblyzer -> librosa -> soundfile 需要 libsndfile；curl 供健康检查
RUN apt-get update && apt-get install -y --no-install-recommends \
      libsndfile1 curl \
 && rm -rf /var/lib/apt/lists/*

COPY services/voiceprint/requirements.txt ./
# 先装 CPU 版 torch（不拉 CUDA，体积小很多），再装其余依赖
RUN pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu \
 && pip install --no-cache-dir -r requirements.txt

COPY services/voiceprint/ ./

ENV PORT=8101 HOST=0.0.0.0 SPEAKER_THRESH=0.75
EXPOSE 8101
# torch + 模型加载较慢，给足 start-period
HEALTHCHECK --interval=15s --timeout=3s --retries=10 --start-period=180s \
  CMD curl -fsS http://localhost:8101/health || exit 1
CMD ["sh","-c","uvicorn app:app --host ${HOST:-0.0.0.0} --port ${PORT:-8101}"]
