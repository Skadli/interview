#!/usr/bin/env bash
# 服务器一键发布：拉取 GHCR 最新镜像并重启。
# 用法： ./scripts/deploy.sh [tag]   默认 latest
set -euo pipefail

cd "$(dirname "$0")/.."

export TAG="${1:-latest}"
: "${IMAGE_OWNER:=skadli}"
export IMAGE_OWNER

if [ ! -f .env ]; then
  echo "缺少 .env，请先 cp .env.example .env 并填入火山/豆包 key" >&2
  exit 1
fi

echo "==> 拉取镜像 (owner=$IMAGE_OWNER tag=$TAG)"
docker compose -f docker-compose.prod.yml pull

echo "==> 启动/更新服务"
docker compose -f docker-compose.prod.yml up -d

echo "==> 清理旧镜像"
docker image prune -f

echo "==> 当前状态"
docker compose -f docker-compose.prod.yml ps
echo "完成。打开 http://<服务器IP>:8000 （手机用麦克风需 HTTPS）"
