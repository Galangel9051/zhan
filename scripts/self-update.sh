#!/bin/sh
# Self-update sidecar for Zhan. It lives outside the app container, so it
# survives the app restart triggered by its own deployment.
set -eu

cd /repo

# The repository is bind-mounted and may appear owned by another UID.
git config --global --add safe.directory /repo >/dev/null 2>&1 || true

interval="${ZHAN_SELF_UPDATE_INTERVAL:-300}"
case "$interval" in
  ''|*[!0-9]*) interval=300 ;;
esac

log() {
  printf '%s [self-update] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*"
}

while :; do
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    log "工作区有未提交改动，跳过本轮"
  else
    old="$(git rev-parse HEAD 2>/dev/null || true)"
    if [ -z "$old" ]; then
      log "无法读取当前提交，跳过本轮"
    elif git fetch --quiet origin main 2>/tmp/self-update-fetch.err; then
      new="$(git rev-parse FETCH_HEAD)"
      if [ "$old" = "$new" ]; then
        log "已是最新 $old"
      else
        log "发现新提交 $old -> $new，开始更新"
        if git merge --ff-only "$new" >/dev/null 2>&1; then
          if docker compose -f compose.yaml --project-directory /repo up -d --build --wait app; then
            log "更新成功：$new"
          else
            log "部署失败，源代码回退到 $old；app 继续使用旧镜像"
            git reset --hard "$old" >/dev/null 2>&1 || true
          fi
        else
          log "快进合并失败，跳过；请检查工作区"
        fi
      fi
    else
      log "拉取失败，稍后重试：$(tr '\n' ' ' </tmp/self-update-fetch.err | cut -c1-300)"
    fi
  fi
  if [ "${ZHAN_SELF_UPDATE_ONCE:-0}" = "1" ]; then
    exit 0
  fi
  sleep "$interval"
done
