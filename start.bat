@echo off
setlocal
cd /d %~dp0

if not exist .venv (
    python -m venv .venv
)

call .venv\Scripts\activate.bat
python -m pip install --upgrade pip
pip install -r requirements.txt

if "%HOST%"=="" set HOST=0.0.0.0
if "%PORT%"=="" set PORT=5000
if "%DEBUG%"=="" set DEBUG=true

python generate_directory.py . -r
python admin_server.py
