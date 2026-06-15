#!/usr/bin/env bash
# 打版本标签并推送，触发 GitHub Actions 构建+发布镜像。
# 用法： ./scripts/release.sh v1.0.0
set -euo pipefail

TAG="${1:?用法: ./scripts/release.sh vX.Y.Z}"
case "$TAG" in
  v*) ;;
  *) echo "标签需以 v 开头，如 v1.0.0" >&2; exit 1;;
esac

git tag -a "$TAG" -m "release $TAG"
git push origin "$TAG"
echo "已推送标签 $TAG，GitHub Actions 将构建并发布镜像到 GHCR。"
echo "进度： https://github.com/Skadli/interview/actions"
