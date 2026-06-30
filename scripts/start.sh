#!/usr/bin/env bash
set -euo pipefail

APP_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$APP_DIR"

PYTHON_BIN="${PYTHON_BIN:-python3}"
VENV_DIR="${VENV_DIR:-$APP_DIR/.venv}"
HOST="${HOST:-127.0.0.1}"
PORT="${PORT:-5000}"

if [ ! -d "$VENV_DIR" ]; then
  "$PYTHON_BIN" -m venv "$VENV_DIR"
fi

source "$VENV_DIR/bin/activate"
python -m pip install --upgrade pip
pip install -r requirements.txt

if [ ! -f "$APP_DIR/.secret_key" ]; then
  python - <<'PY'
import secrets
from pathlib import Path

Path(".secret_key").write_text(secrets.token_hex(32), encoding="utf-8")
PY
fi

export SECRET_KEY="${SECRET_KEY:-$(cat "$APP_DIR/.secret_key")}"
export HOST
export PORT
export DEBUG="${DEBUG:-false}"

python generate_directory.py . -r
exec "$VENV_DIR/bin/gunicorn" -c gunicorn.conf.py wsgi:app
