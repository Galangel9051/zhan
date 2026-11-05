#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
if ! command -v node >/dev/null 2>&1 && [ -s "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ]; then
  source "${NVM_DIR:-$HOME/.nvm}/nvm.sh"
fi
command -v node >/dev/null 2>&1 || { echo '需要 Node.js 24；请先运行 nvm install && nvm use。' >&2; exit 1; }
command -v go >/dev/null 2>&1 || { echo '需要 Go 1.26+。' >&2; exit 1; }
node -e 'if(Number(process.versions.node.split(".")[0]) < 24) { console.error("需要 Node.js 24+"); process.exit(1) }'
npm --prefix frontend ci
npm --prefix frontend run build
node scripts/prepare-web.mjs
mkdir -p bin
cd backend
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o ../bin/zhan ./cmd/server
printf '\n构建完成：%s/bin/zhan\n' "$project_root"
