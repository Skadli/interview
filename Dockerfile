# syntax=docker/dockerfile:1
# 三合一单镜像：前端 + Go实时核心 + 声纹 + 上下文，单容器内由 supervisor 拉起三进程。
# 体积优化：多阶段(编译器/构建工具不进运行层)、slim 基础、--no-cache、清 pyc、Go 静态二进制 -s -w。
# 可选小镜像：--build-arg WITH_TORCH=0  → 不装 torch(声纹降级)，体积大幅减小。

ARG WITH_TORCH=1

# ---- 1) 前端 ----
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci 2>/dev/null || npm install
COPY web/ ./
RUN npm run build

# ---- 2) Go 静态二进制 ----
FROM golang:1.26 AS go
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct CGO_ENABLED=0 GOOS=linux
COPY backend-go/go.mod backend-go/go.sum ./
RUN go mod download
COPY backend-go/ ./
RUN go build -ldflags="-s -w" -o /out/interview-backend .

# ---- 3) Python 依赖（带编译器，产出干净 venv 供运行层复制） ----
FROM python:3.12-slim AS py
ARG WITH_TORCH
RUN apt-get update && apt-get install -y --no-install-recommends build-essential libsndfile1 \
 && rm -rf /var/lib/apt/lists/*
RUN python -m venv /opt/venv
ENV PATH=/opt/venv/bin:$PATH
COPY services/voiceprint/requirements.txt /tmp/vp.txt
COPY services/context/requirements.txt /tmp/ctx.txt
RUN pip install --no-cache-dir --upgrade pip supervisor \
 && pip install --no-cache-dir -r /tmp/ctx.txt \
 && if [ "$WITH_TORCH" = "1" ]; then \
        pip install --no-cache-dir torch --index-url https://download.pytorch.org/whl/cpu \
     && pip install --no-cache-dir -r /tmp/vp.txt ; \
    else \
        pip install --no-cache-dir fastapi "uvicorn[standard]" numpy ; \
    fi \
 && find /opt/venv -type d -name '__pycache__' -prune -exec rm -rf {} + \
 && find /opt/venv -type f -name '*.pyc' -delete

# ---- 4) 运行层（无编译器，仅运行时库） ----
FROM python:3.12-slim
RUN apt-get update && apt-get install -y --no-install-recommends libsndfile1 ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
ENV PATH=/opt/venv/bin:$PATH \
    FRONTEND_DIR=/app/web/dist \
    ADDR=:8000 \
    SPEAKER_SIDECAR_URL=http://127.0.0.1:8101 \
    CONTEXT_URL=http://127.0.0.1:8102
COPY --from=py /opt/venv /opt/venv
COPY --from=go /out/interview-backend /app/interview-backend
COPY --from=web /web/dist /app/web/dist
COPY services/voiceprint /app/voiceprint
COPY services/context /app/context
COPY deploy/supervisord.conf /etc/supervisord.conf
WORKDIR /app
EXPOSE 8000
# 声纹加载 torch 较慢，给足 start-period（backend 8000 自身很快）
HEALTHCHECK --interval=15s --timeout=3s --retries=5 --start-period=180s \
  CMD curl -fsS http://localhost:8000/health || exit 1
CMD ["supervisord", "-c", "/etc/supervisord.conf"]
