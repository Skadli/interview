# syntax=docker/dockerfile:1
# 上下文服务（简历解析 + 公司简报）：轻依赖，无 GPU、无重型 ML。
# 构建上下文为项目根目录（见 docker-compose.yml: context: .）
FROM python:3.12-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends curl \
 && rm -rf /var/lib/apt/lists/*

COPY services/context/requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY services/context/ ./

ENV PORT=8102 HOST=0.0.0.0
EXPOSE 8102
HEALTHCHECK --interval=15s --timeout=3s --retries=5 --start-period=20s \
  CMD curl -fsS http://localhost:8102/health || exit 1
CMD ["sh","-c","uvicorn app:app --host ${HOST:-0.0.0.0} --port ${PORT:-8102}"]
