#!/usr/bin/env bash
set -euo pipefail

APP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$APP_DIR"

PYTHON_BIN="${PYTHON_BIN:-python3}"
if [ -d "$APP_DIR/.venv" ]; then
  source "$APP_DIR/.venv/bin/activate"
  PYTHON_BIN="python"
fi

"$PYTHON_BIN" generate_directory.py . -r
