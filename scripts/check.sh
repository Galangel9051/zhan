#!/usr/bin/env bash
set -euo pipefail
project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$project_root"
if ! command -v node >/dev/null 2>&1 && [ -s "${NVM_DIR:-$HOME/.nvm}/nvm.sh" ]; then source "${NVM_DIR:-$HOME/.nvm}/nvm.sh"; fi
(cd backend && go test ./... && go vet ./...)
npm --prefix frontend test
npm --prefix frontend run build
