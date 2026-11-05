#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
if ! command -v node >/dev/null 2>&1 && [ -s "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ]; then source "${NVM_DIR:-$HOME/.nvm}/nvm.sh"; fi
unformatted="$(gofmt -l backend)"
if [ -n "$unformatted" ]; then
  printf '以下 Go 文件需要格式化，请运行 gofmt -w backend：\n%s\n' "$unformatted" >&2
  exit 1
fi
(cd backend && go test ./... && go vet ./...)
npm --prefix frontend test
npm --prefix frontend run build
