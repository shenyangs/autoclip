#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLI_DIR="$ROOT/cli"

if command -v autoclip >/dev/null 2>&1; then
  command -v autoclip
  exit 0
fi

if [ -x "$CLI_DIR/autoclip" ]; then
  echo "$CLI_DIR/autoclip"
  exit 0
fi

if command -v go >/dev/null 2>&1; then
  (cd "$CLI_DIR" && go build -o autoclip ./cmd/autoclip)
  echo "$CLI_DIR/autoclip"
  exit 0
fi

cat >&2 <<'EOF'
未找到 autoclip，也未找到 Go 工具链来构建它。
请先安装 Go，或在 cli/ 目录构建 autoclip 后重试。
EOF
exit 4
