# syntax=docker/dockerfile:1
# 多阶段：构建前端 -> 构建 Go -> 极简运行时（内含前端静态产物）
# 构建上下文为项目根目录（见 docker-compose.yml: context: .）

# ---- 1) 构建前端 web/dist ----
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* ./
RUN npm ci 2>/dev/null || npm install
COPY web/ ./
RUN npm run build

# ---- 2) 构建 Go 静态二进制 ----
FROM golang:1.26 AS gobuild
WORKDIR /src
ENV GOPROXY=https://goproxy.cn,direct CGO_ENABLED=0 GOOS=linux
COPY backend-go/go.mod backend-go/go.sum ./
RUN go mod download
COPY backend-go/ ./
RUN go build -ldflags="-s -w" -o /out/interview-backend .

# ---- 3) 运行时（alpine + CA 证书，供出站 wss/https 调用火山）----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates wget
WORKDIR /app
COPY --from=gobuild /out/interview-backend /app/interview-backend
COPY --from=web /web/dist /app/web/dist
ENV ADDR=:8000 FRONTEND_DIR=/app/web/dist
EXPOSE 8000
HEALTHCHECK --interval=15s --timeout=3s --retries=5 \
  CMD wget -qO- http://localhost:8000/health || exit 1
ENTRYPOINT ["/app/interview-backend"]
