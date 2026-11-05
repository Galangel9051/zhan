#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
cd "$project_root"
command -v docker >/dev/null 2>&1 || { echo '请先安装并启动 Docker。' >&2; exit 1; }
docker compose version >/dev/null
# Resolve symlinks before mounting: Compose passes these paths to the host daemon.
projects_root="${ZHAN_HOST_PROJECTS_ROOT:-$(dirname "$project_root")}"
data_root="${ZHAN_HOST_DATA_DIR:-${ZHAN_DATA_DIR:-$project_root/data}}"
if [ -n "${ZHAN_HOST_DATA_DIR:-}" ] && [ -n "${ZHAN_DATA_DIR:-}" ] && [ "$ZHAN_HOST_DATA_DIR" != "$ZHAN_DATA_DIR" ]; then
  echo 'ZHAN_HOST_DATA_DIR 与 ZHAN_DATA_DIR 必须指向同一目录。' >&2
  exit 1
fi
[ -d "$projects_root" ] || { echo "项目根目录不存在：$projects_root" >&2; exit 1; }
mkdir -p "$data_root"
export ZHAN_HOST_PROJECTS_ROOT="$(cd "$projects_root" && pwd -P)"
export ZHAN_HOST_DATA_DIR="$(cd "$data_root" && pwd -P)"
export ZHAN_HOST_REPO="$project_root"
if [ "$#" -eq 0 ]; then
  set -- up -d --build --wait
fi
exec docker compose -f "$project_root/compose.yaml" "$@"
