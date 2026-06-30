#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import json
import os
import hashlib
import shutil
import subprocess
import sys
import secrets
from pathlib import Path
from datetime import datetime
from functools import wraps

from flask import Flask, render_template_string, request, jsonify, send_from_directory, session, redirect, url_for
from generate_directory import DirectoryGenerator

app = Flask(__name__)
BASE_DIR = Path(__file__).parent.resolve()
CONFIG_PATH = BASE_DIR / "config.json"
STATS_PATH = BASE_DIR / "access_stats.json"
AUTH_PATH = BASE_DIR / "admin_auth.json"

app.secret_key = os.environ.get("SECRET_KEY") or secrets.token_hex(32)


def hash_password(password):
    return hashlib.sha256(password.encode('utf-8')).hexdigest()


def load_auth_config():
    default_auth = {
        "username": "admin",
        "password_hash": hash_password("admin123")
    }
    try:
        with open(AUTH_PATH, 'r', encoding='utf-8') as f:
            data = json.load(f)
            if 'password_hash' not in data and 'password' in data:
                data['password_hash'] = hash_password(data['password'])
                del data['password']
                save_auth_config(data)
            return data
    except (FileNotFoundError, json.JSONDecodeError):
        save_auth_config(default_auth)
        return default_auth


def save_auth_config(auth_data):
    with open(AUTH_PATH, 'w', encoding='utf-8') as f:
        json.dump(auth_data, f, ensure_ascii=False, indent=2)


def login_required(f):
    @wraps(f)
    def decorated_function(*args, **kwargs):
        if not session.get('logged_in'):
            if request.path.startswith('/api/'):
                return jsonify({"error": "未登录", "code": 401}), 401
            return redirect(url_for('login'))
        return f(*args, **kwargs)
    return decorated_function


def load_config():
    try:
        with open(CONFIG_PATH, 'r', encoding='utf-8') as f:
            return json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        return {}


def save_config(config):
    with open(CONFIG_PATH, 'w', encoding='utf-8') as f:
        json.dump(config, f, ensure_ascii=False, indent=4)


def load_stats():
    try:
        with open(STATS_PATH, 'r', encoding='utf-8') as f:
            return json.load(f)
    except (FileNotFoundError, json.JSONDecodeError):
        return {"total_visits": 0, "daily": {}, "pages": {}, "downloads": {}}


def save_stats(stats):
    with open(STATS_PATH, 'w', encoding='utf-8') as f:
        json.dump(stats, f, ensure_ascii=False, indent=2)


def record_visit(page="/"):
    stats = load_stats()
    stats["total_visits"] = stats.get("total_visits", 0) + 1
    today = datetime.now().strftime("%Y-%m-%d")
    stats.setdefault("daily", {})
    stats["daily"][today] = stats["daily"].get(today, 0) + 1
    stats.setdefault("pages", {})
    stats["pages"][page] = stats["pages"].get(page, 0) + 1
    save_stats(stats)


def record_download(filename):
    stats = load_stats()
    stats.setdefault("downloads", {})
    stats["downloads"][filename] = stats["downloads"].get(filename, 0) + 1
    save_stats(stats)


def to_relative_path(path_obj):
    try:
        relative = path_obj.resolve().relative_to(BASE_DIR)
    except ValueError:
        return ""
    return str(relative).replace("\\", "/") if str(relative) != "." else ""


def is_under_base(path_obj):
    resolved = path_obj.resolve()
    return resolved == BASE_DIR or BASE_DIR in resolved.parents


def resolve_relative_directory(relative_path=""):
    raw_path = (relative_path or "").strip().replace("\\", "/")
    if raw_path in ("", ".", "/"):
        return BASE_DIR

    parts = [part for part in raw_path.split("/") if part not in ("", ".")]
    if any(part == ".." for part in parts):
        raise ValueError("目录路径非法")

    target_path = (BASE_DIR / Path(*parts)).resolve()
    if not is_under_base(target_path):
        raise ValueError("目录路径超出站点根目录")
    return target_path


def sanitize_upload_filename(filename):
    name = os.path.basename((filename or "").strip().replace("\x00", ""))
    if not name or name in {".", ".."}:
        return ""
    if "/" in name or "\\" in name:
        return ""
    return name


def sanitize_directory_name(name):
    clean_name = (name or "").strip().replace("\x00", "")
    if not clean_name or clean_name in {".", ".."}:
        return ""
    if "/" in clean_name or "\\" in clean_name:
        return ""
    return clean_name


def resolve_relative_path(relative_path=""):
    raw_path = (relative_path or "").strip().replace("\\", "/")
    if raw_path in ("", ".", "/"):
        return BASE_DIR

    parts = [part for part in raw_path.split("/") if part not in ("", ".")]
    if any(part == ".." for part in parts):
        raise ValueError("路径非法")

    target_path = (BASE_DIR / Path(*parts)).resolve()
    if not is_under_base(target_path):
        raise ValueError("路径超出站点根目录")
    return target_path


def should_include_entry(path_obj):
    config = load_config()
    exclude_files = config.get("exclude", {}).get("files", [])
    exclude_exts = config.get("exclude", {}).get("extensions", [])
    name = path_obj.name
    name_lower = name.lower()

    if name.startswith((".", "~", "_")):
        return False

    for pattern in exclude_files:
        if name_lower == str(pattern).lower():
            return False

    for pattern in exclude_exts:
        pattern = str(pattern).lower()
        if pattern.startswith(".") and name_lower.endswith(pattern):
            return False

    return True


def get_directory_payload(directory_path):
    entries = []
    for item in sorted(directory_path.iterdir(), key=lambda entry: (not entry.is_dir(), entry.name.lower())):
        if not should_include_entry(item):
            continue
        stat = item.stat()
        entries.append({
            "name": item.name,
            "type": "dir" if item.is_dir() else "file",
            "relative_path": to_relative_path(item),
            "size": 0 if item.is_dir() else stat.st_size,
            "modified_at": datetime.fromtimestamp(stat.st_mtime).strftime("%Y-%m-%d %H:%M:%S"),
        })

    current_path = to_relative_path(directory_path)
    parent_path = to_relative_path(directory_path.parent) if directory_path != BASE_DIR else ""

    return {
        "current_path": current_path,
        "current_label": "/" if not current_path else f"/{current_path}",
        "parent_path": parent_path,
        "entry_count": len(entries),
        "entries": entries,
    }


def regenerate_directory_chain(directory_path):
    generator = DirectoryGenerator(BASE_DIR, config_path=CONFIG_PATH)
    targets = []
    current = directory_path.resolve()

    while True:
        if current == BASE_DIR or BASE_DIR in current.parents:
            targets.append(current)
        if current == BASE_DIR:
            break
        current = current.parent

    targets.reverse()
    generated = 0
    for target in targets:
        if generator.generate_directory_html(target):
            generated += 1
    return generated


LOGIN_HTML = '''
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>管理后台 - 登录</title>
    <style>
        :root {
            --primary: #2563eb;
            --primary-dark: #1d4ed8;
            --primary-light: #dbeafe;
            --danger: #dc2626;
            --gray-50: #f9fafb;
            --gray-100: #f3f4f6;
            --gray-200: #e5e7eb;
            --gray-300: #d1d5db;
            --gray-400: #9ca3af;
            --gray-500: #6b7280;
            --gray-600: #4b5563;
            --gray-700: #374151;
            --gray-800: #1f2937;
            --radius: 8px;
        }
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Noto Sans SC", sans-serif;
            background: linear-gradient(135deg, #eff6ff 0%, #f0fdf4 100%);
            min-height: 100vh;
            display: flex;
            align-items: center;
            justify-content: center;
        }
        .login-card {
            background: #fff;
            border: 1px solid var(--gray-200);
            border-radius: 12px;
            padding: 40px 36px;
            width: 100%;
            max-width: 400px;
            box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1), 0 2px 4px -1px rgba(0,0,0,0.06);
        }
        .login-card .logo {
            text-align: center;
            margin-bottom: 24px;
        }
        .login-card .logo .icon {
            font-size: 48px;
            margin-bottom: 8px;
        }
        .login-card .logo h1 {
            font-size: 20px;
            color: var(--gray-800);
            font-weight: 700;
        }
        .login-card .logo p {
            font-size: 13px;
            color: var(--gray-400);
            margin-top: 4px;
        }
        .form-group { margin-bottom: 20px; }
        .form-group label {
            display: block;
            font-size: 13px;
            font-weight: 600;
            color: var(--gray-600);
            margin-bottom: 6px;
        }
        .form-group input {
            width: 100%;
            padding: 10px 14px;
            border: 1px solid var(--gray-300);
            border-radius: 8px;
            font-size: 15px;
            outline: none;
            transition: border-color 0.2s, box-shadow 0.2s;
        }
        .form-group input:focus {
            border-color: var(--primary);
            box-shadow: 0 0 0 3px var(--primary-light);
        }
        .login-btn {
            width: 100%;
            padding: 12px;
            border: none;
            border-radius: 8px;
            background: linear-gradient(135deg, var(--primary) 0%, var(--primary-dark) 100%);
            color: #fff;
            font-size: 15px;
            font-weight: 600;
            cursor: pointer;
            transition: opacity 0.2s;
        }
        .login-btn:hover { opacity: 0.9; }
        .login-btn:active { opacity: 0.8; }
        .error-msg {
            background: #fef2f2;
            border: 1px solid #fecaca;
            color: var(--danger);
            padding: 10px 14px;
            border-radius: 6px;
            font-size: 13px;
            margin-bottom: 16px;
            display: none;
        }
        .back-link {
            text-align: center;
            margin-top: 20px;
        }
        .back-link a {
            color: var(--gray-400);
            text-decoration: none;
            font-size: 13px;
        }
        .back-link a:hover { color: var(--primary); }
        .default-hint {
            text-align: center;
            margin-top: 16px;
            font-size: 11px;
            color: var(--gray-400);
        }
    </style>
</head>
<body>
    <div class="login-card">
        <div class="logo">
            <div class="icon">🔐</div>
            <h1>管理后台登录</h1>
            <p>请输入管理员账号和密码</p>
        </div>
        {% if error %}
        <div class="error-msg" style="display:block">{{ error }}</div>
        {% endif %}
        <form method="POST" action="/login">
            <div class="form-group">
                <label>👤 用户名</label>
                <input type="text" name="username" placeholder="请输入用户名" required autofocus>
            </div>
            <div class="form-group">
                <label>🔑 密码</label>
                <input type="password" name="password" placeholder="请输入密码" required>
            </div>
            <button type="submit" class="login-btn">登 录</button>
        </form>
        <div class="back-link">
            <a href="/">← 返回站点首页</a>
        </div>
        <div class="default-hint">
            默认账号: admin / admin123（请及时修改密码）
        </div>
    </div>
</body>
</html>
'''


ADMIN_HTML = '''
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>站点管理后台</title>
    <style>
        :root {
            --primary: #2563eb;
            --primary-dark: #1d4ed8;
            --primary-light: #dbeafe;
            --success: #16a34a;
            --danger: #dc2626;
            --warning: #f59e0b;
            --gray-50: #f9fafb;
            --gray-100: #f3f4f6;
            --gray-200: #e5e7eb;
            --gray-300: #d1d5db;
            --gray-400: #9ca3af;
            --gray-500: #6b7280;
            --gray-600: #4b5563;
            --gray-700: #374151;
            --gray-800: #1f2937;
            --radius: 8px;
        }
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Noto Sans SC", sans-serif;
            background: var(--gray-50);
            color: var(--gray-800);
            line-height: 1.6;
        }
        .admin-header {
            background: linear-gradient(135deg, var(--primary) 0%, var(--primary-dark) 100%);
            color: #fff;
            padding: 16px 24px;
            display: flex;
            align-items: center;
            justify-content: space-between;
        }
        .admin-header h1 { font-size: 20px; font-weight: 700; }
        .admin-header .header-right { display: flex; align-items: center; gap: 16px; }
        .admin-header a {
            color: rgba(255,255,255,0.8);
            text-decoration: none;
            font-size: 14px;
            transition: color 0.2s;
        }
        .admin-header a:hover { color: #fff; }
        .admin-header .user-info {
            font-size: 13px;
            color: rgba(255,255,255,0.7);
            display: flex;
            align-items: center;
            gap: 6px;
        }
        .logout-btn {
            padding: 4px 12px;
            border: 1px solid rgba(255,255,255,0.3);
            border-radius: 4px;
            background: rgba(255,255,255,0.1);
            color: rgba(255,255,255,0.8);
            font-size: 12px;
            cursor: pointer;
            text-decoration: none;
            transition: all 0.2s;
        }
        .logout-btn:hover {
            background: rgba(255,255,255,0.2);
            color: #fff;
            border-color: rgba(255,255,255,0.5);
        }
        .admin-container { max-width: 960px; margin: 24px auto; padding: 0 24px; }
        .card {
            background: #fff;
            border: 1px solid var(--gray-200);
            border-radius: var(--radius);
            padding: 24px;
            margin-bottom: 20px;
            box-shadow: 0 1px 2px rgba(0,0,0,0.05);
        }
        .card h2 {
            font-size: 16px;
            color: var(--gray-800);
            margin-bottom: 16px;
            padding-bottom: 8px;
            border-bottom: 1px solid var(--gray-200);
        }
        .form-group { margin-bottom: 16px; }
        .form-group label {
            display: block;
            font-size: 13px;
            font-weight: 600;
            color: var(--gray-600);
            margin-bottom: 4px;
        }
        .form-group input, .form-group textarea, .form-group select {
            width: 100%;
            padding: 8px 12px;
            border: 1px solid var(--gray-300);
            border-radius: 6px;
            font-size: 14px;
            outline: none;
            transition: border-color 0.2s;
        }
        .form-group input:focus, .form-group textarea:focus {
            border-color: var(--primary);
            box-shadow: 0 0 0 3px var(--primary-light);
        }
        .form-group textarea { min-height: 80px; resize: vertical; }
        .form-row { display: flex; gap: 12px; }
        .form-row .form-group { flex: 1; }
        .btn {
            padding: 8px 20px;
            border: none;
            border-radius: 6px;
            font-size: 14px;
            font-weight: 500;
            cursor: pointer;
            transition: all 0.2s;
        }
        .btn-primary { background: var(--primary); color: #fff; }
        .btn-primary:hover { background: var(--primary-dark); }
        .btn-success { background: var(--success); color: #fff; }
        .btn-success:hover { background: #15803d; }
        .btn-danger { background: var(--danger); color: #fff; }
        .btn-danger:hover { background: #b91c1c; }
        .btn-warning { background: var(--warning); color: #fff; }
        .btn-warning:hover { background: #d97706; }
        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
            gap: 12px;
            margin-bottom: 16px;
        }
        .stat-card {
            background: var(--gray-50);
            border: 1px solid var(--gray-200);
            border-radius: 6px;
            padding: 16px;
            text-align: center;
        }
        .stat-card .stat-value {
            font-size: 28px;
            font-weight: 700;
            color: var(--primary);
        }
        .stat-card .stat-label {
            font-size: 12px;
            color: var(--gray-500);
            margin-top: 4px;
        }
        .toast {
            position: fixed;
            top: 20px;
            right: 20px;
            padding: 12px 20px;
            border-radius: 6px;
            color: #fff;
            font-size: 14px;
            font-weight: 500;
            z-index: 9999;
            display: none;
        }
        .toast.success { background: var(--success); }
        .toast.error { background: var(--danger); }
        .icon-list {
            display: grid;
            grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
            gap: 8px;
        }
        .icon-item {
            display: flex;
            align-items: center;
            gap: 8px;
            padding: 6px 10px;
            background: var(--gray-50);
            border: 1px solid var(--gray-200);
            border-radius: 4px;
            font-size: 13px;
        }
        .icon-item .ext { font-weight: 600; color: var(--gray-700); min-width: 60px; }
        .icon-item .icon-emoji { font-size: 18px; }
        .tabs { display: flex; gap: 4px; margin-bottom: 20px; flex-wrap: wrap; }
        .tab-btn {
            padding: 8px 16px;
            border: 1px solid var(--gray-300);
            border-radius: 6px 6px 0 0;
            background: var(--gray-100);
            font-size: 14px;
            cursor: pointer;
            color: var(--gray-600);
        }
        .tab-btn.active { background: #fff; border-bottom-color: #fff; color: var(--primary); font-weight: 600; }
        .tab-content { display: none; }
        .tab-content.active { display: block; }
        .top-list { list-style: none; }
        .top-list li {
            display: flex;
            justify-content: space-between;
            padding: 6px 0;
            border-bottom: 1px solid var(--gray-100);
            font-size: 13px;
        }
        .top-list li .count { font-weight: 600; color: var(--primary); }
        .password-strength {
            height: 4px;
            border-radius: 2px;
            margin-top: 4px;
            background: var(--gray-200);
            overflow: hidden;
        }
        .password-strength .bar {
            height: 100%;
            border-radius: 2px;
            transition: width 0.3s, background 0.3s;
        }
        .path-toolbar {
            display: flex;
            gap: 10px;
            flex-wrap: wrap;
            align-items: center;
            margin-bottom: 16px;
        }
        .path-chip {
            display: inline-flex;
            align-items: center;
            min-height: 38px;
            padding: 0 12px;
            border-radius: 6px;
            background: var(--gray-100);
            border: 1px solid var(--gray-200);
            font-size: 13px;
            color: var(--gray-700);
        }
        .file-browser {
            border: 1px solid var(--gray-200);
            border-radius: 8px;
            overflow: hidden;
        }
        .file-browser table {
            width: 100%;
            border-collapse: collapse;
        }
        .file-browser th,
        .file-browser td {
            padding: 10px 12px;
            border-bottom: 1px solid var(--gray-100);
            font-size: 13px;
            text-align: left;
        }
        .file-browser th {
            background: var(--gray-50);
            color: var(--gray-600);
            font-weight: 600;
        }
        .file-browser tbody tr:hover {
            background: #f8fbff;
        }
        .file-link-btn {
            background: none;
            border: none;
            color: var(--primary);
            cursor: pointer;
            font-size: 13px;
            padding: 0;
        }
        .file-link-btn:hover {
            text-decoration: underline;
        }
        .file-type-badge {
            display: inline-block;
            min-width: 42px;
            text-align: center;
            padding: 2px 8px;
            border-radius: 999px;
            background: var(--gray-100);
            color: var(--gray-600);
            font-size: 12px;
        }
        .upload-hint {
            font-size: 12px;
            color: var(--gray-500);
            margin-top: 8px;
        }
        .upload-result {
            margin-top: 12px;
            padding: 12px;
            border-radius: 6px;
            background: var(--gray-50);
            border: 1px solid var(--gray-200);
            font-size: 13px;
            color: var(--gray-700);
            white-space: pre-wrap;
            word-break: break-word;
        }
        .browser-actions {
            display: flex;
            gap: 8px;
            flex-wrap: wrap;
        }
        .btn-sm {
            padding: 5px 10px;
            font-size: 12px;
        }
        .drop-zone {
            border: 2px dashed var(--gray-300);
            border-radius: 8px;
            background: var(--gray-50);
            padding: 22px 18px;
            text-align: center;
            transition: all 0.2s;
            cursor: pointer;
        }
        .drop-zone.dragover {
            border-color: var(--primary);
            background: #eff6ff;
        }
        .drop-zone strong {
            display: block;
            color: var(--gray-800);
            font-size: 15px;
            margin-bottom: 6px;
        }
        .drop-zone span {
            color: var(--gray-500);
            font-size: 12px;
        }
        .progress-wrap {
            margin-top: 12px;
            display: none;
        }
        .progress-text {
            display: flex;
            justify-content: space-between;
            font-size: 12px;
            color: var(--gray-600);
            margin-bottom: 6px;
        }
        .progress-bar {
            height: 10px;
            border-radius: 999px;
            background: var(--gray-200);
            overflow: hidden;
        }
        .progress-bar > div {
            height: 100%;
            width: 0;
            background: linear-gradient(135deg, var(--primary) 0%, var(--primary-dark) 100%);
            transition: width 0.15s ease;
        }
    </style>
</head>
<body>
    <div class="admin-header">
        <h1>⚙️ 站点管理后台</h1>
        <div class="header-right">
            <span class="user-info">👤 {{ username }}</span>
            <a href="/" class="logout-btn">🏠 站点首页</a>
            <a href="/logout" class="logout-btn">🚪 退出登录</a>
        </div>
    </div>

    <div class="admin-container">
        <div class="tabs">
            <button class="tab-btn active" onclick="switchTab('config', event)">📝 站点配置</button>
            <button class="tab-btn" onclick="switchTab('icons', event)">🎨 图标映射</button>
            <button class="tab-btn" onclick="switchTab('files', event)">📁 文件管理</button>
            <button class="tab-btn" onclick="switchTab('stats', event)">📊 访问统计</button>
            <button class="tab-btn" onclick="switchTab('generate', event)">🔄 重新生成</button>
            <button class="tab-btn" onclick="switchTab('account', event)">🔑 账号管理</button>
        </div>

        <div id="tab-config" class="tab-content active">
            <div class="card">
                <h2>站点基本信息</h2>
                <div class="form-group">
                    <label>站点标题</label>
                    <input type="text" id="site_title" placeholder="站点标题">
                </div>
                <div class="form-group">
                    <label>站点URL</label>
                    <input type="text" id="site_url" placeholder="https://dl.100ask.net">
                </div>
            </div>
            <div class="card">
                <h2>介绍区域</h2>
                <div class="form-group">
                    <label>介绍标题</label>
                    <input type="text" id="intro_title" placeholder="介绍标题">
                </div>
                <div class="form-group">
                    <label>介绍内容</label>
                    <textarea id="intro_content" placeholder="介绍内容"></textarea>
                </div>
                <div class="form-group">
                    <label>启用介绍区域</label>
                    <select id="intro_enabled">
                        <option value="true">启用</option>
                        <option value="false">禁用</option>
                    </select>
                </div>
            </div>
            <div class="card">
                <h2>页脚信息</h2>
                <div class="form-group">
                    <label>版权信息</label>
                    <input type="text" id="footer_copyright" placeholder="版权信息">
                </div>
            </div>
            <button class="btn btn-primary" onclick="saveConfig()">💾 保存配置</button>
        </div>

        <div id="tab-icons" class="tab-content">
            <div class="card">
                <h2>文件类型图标映射</h2>
                <div class="icon-list" id="iconList"></div>
            </div>
            <div class="card">
                <h2>添加自定义图标映射</h2>
                <div class="form-group">
                    <label>文件扩展名（如 .xyz）</label>
                    <input type="text" id="new_ext" placeholder=".xyz">
                </div>
                <div class="form-group">
                    <label>图标类型</label>
                    <select id="new_icon">
                        <option value="file">📄 文件</option>
                        <option value="archive">📦 压缩包</option>
                        <option value="disc">💿 镜像</option>
                        <option value="pdf">📕 PDF</option>
                        <option value="code">💻 代码</option>
                        <option value="image">🖼️ 图片</option>
                        <option value="video">🎬 视频</option>
                        <option value="data">📊 数据</option>
                        <option value="model">🧠 模型</option>
                        <option value="binary">⚙️ 二进制</option>
                        <option value="build">🔧 构建</option>
                        <option value="text">📝 文本</option>
                    </select>
                </div>
                <button class="btn btn-success" onclick="addIconMapping()">➕ 添加映射</button>
            </div>
        </div>

        <div id="tab-stats" class="tab-content">
            <div class="card">
                <h2>访问统计概览</h2>
                <div class="stats-grid" id="statsGrid"></div>
            </div>
            <div class="card">
                <h2>热门页面 TOP 10</h2>
                <ol class="top-list" id="topPages"></ol>
            </div>
            <div class="card">
                <h2>热门下载 TOP 10</h2>
                <ol class="top-list" id="topDownloads"></ol>
            </div>
        </div>

        <div id="tab-files" class="tab-content">
            <div class="card">
                <h2>目录浏览</h2>
                <div class="path-toolbar">
                    <div class="path-chip" id="currentPathLabel">当前目录：/</div>
                    <button class="btn btn-primary" onclick="loadFileBrowser(browserState.path)">刷新目录</button>
                    <button class="btn btn-success" onclick="openParentDirectory()">返回上级</button>
                    <a class="logout-btn" id="openSiteDir" href="/" target="_blank" style="color:var(--primary);border-color:var(--gray-300);background:#fff;">打开前台目录</a>
                </div>
                <div class="file-browser">
                    <table>
                        <thead>
                            <tr>
                                <th style="width:38%;">名称</th>
                                <th style="width:14%;">类型</th>
                                <th style="width:16%;">大小</th>
                                <th style="width:20%;">修改时间</th>
                                <th style="width:12%;">操作</th>
                            </tr>
                        </thead>
                        <tbody id="fileBrowserBody">
                            <tr><td colspan="5">加载中...</td></tr>
                        </tbody>
                    </table>
                </div>
                <p class="upload-hint" id="browserSummary">用于遍历站点目录并选择上传目标位置。</p>
            </div>
            <div class="card">
                <h2>新建目录</h2>
                <div class="form-group">
                    <label>当前父目录</label>
                    <input type="text" id="mkdir_parent_path" value="/" readonly>
                </div>
                <div class="form-group">
                    <label>目录名称</label>
                    <input type="text" id="new_directory_name" placeholder="例如：Images、工具包、2026-07">
                </div>
                <button class="btn btn-primary" onclick="createDirectory()">📁 新建目录并自动生成页面</button>
            </div>
            <div class="card">
                <h2>上传文件</h2>
                <div class="form-group">
                    <label>上传目标目录</label>
                    <input type="text" id="upload_target_path" value="/" readonly>
                </div>
                <div class="form-group">
                    <label>选择文件</label>
                    <div class="drop-zone" id="uploadDropZone" onclick="document.getElementById('upload_files').click()">
                        <strong>拖拽文件到这里上传</strong>
                        <span>也可以点击这里选择多个文件，上传后会自动刷新目录展示</span>
                    </div>
                    <input type="file" id="upload_files" multiple onchange="handleFileInputChange()" style="display:none">
                    <div class="upload-hint" id="selectedFilesHint">未选择文件</div>
                </div>
                <div class="form-group">
                    <label>覆盖同名文件</label>
                    <select id="upload_overwrite">
                        <option value="false">否，遇到同名文件则跳过</option>
                        <option value="true">是，直接覆盖已有文件</option>
                    </select>
                </div>
                <button class="btn btn-success" onclick="uploadFiles()">⬆️ 上传并自动刷新展示</button>
                <div class="progress-wrap" id="uploadProgressWrap">
                    <div class="progress-text">
                        <span id="uploadProgressLabel">准备上传...</span>
                        <span id="uploadProgressPercent">0%</span>
                    </div>
                    <div class="progress-bar"><div id="uploadProgressBar"></div></div>
                </div>
                <div class="upload-result" id="uploadResultBox" style="display:none;"></div>
            </div>
        </div>

        <div id="tab-generate" class="tab-content">
            <div class="card">
                <h2>重新生成页面</h2>
                <p style="color:var(--gray-500);margin-bottom:16px;font-size:14px;">修改配置后需要重新生成所有页面才能生效。</p>
                <button class="btn btn-primary" onclick="regenerateAll()" style="margin-right:8px;">🔄 重新生成所有页面</button>
                <button class="btn btn-success" onclick="regenerateRoot()">🏠 仅生成首页</button>
            </div>
        </div>

        <div id="tab-account" class="tab-content">
            <div class="card">
                <h2>修改密码</h2>
                <div class="form-group">
                    <label>当前密码</label>
                    <input type="password" id="current_password" placeholder="请输入当前密码">
                </div>
                <div class="form-group">
                    <label>新密码</label>
                    <input type="password" id="new_password" placeholder="请输入新密码（至少6位）" oninput="checkPasswordStrength(this.value)">
                    <div class="password-strength"><div class="bar" id="pwStrengthBar"></div></div>
                </div>
                <div class="form-group">
                    <label>确认新密码</label>
                    <input type="password" id="confirm_password" placeholder="请再次输入新密码">
                </div>
                <button class="btn btn-warning" onclick="changePassword()">🔑 修改密码</button>
            </div>
            <div class="card">
                <h2>当前登录信息</h2>
                <p style="font-size:14px;color:var(--gray-600);">👤 用户名: <strong>{{ username }}</strong></p>
                <p style="font-size:13px;color:var(--gray-400);margin-top:8px;">⚠️ 密码存储在 admin_auth.json 文件中，使用 SHA-256 加密。</p>
            </div>
        </div>
    </div>

    <div class="toast" id="toast"></div>

    <script>
    let config = {};
    let browserState = { path: '', parentPath: '' };
    let selectedUploadFiles = [];

    function switchTab(name, evt) {
        document.querySelectorAll('.tab-content').forEach(t => t.classList.remove('active'));
        document.querySelectorAll('.tab-btn').forEach(b => b.classList.remove('active'));
        document.getElementById('tab-' + name).classList.add('active');
        const trigger = evt && evt.currentTarget ? evt.currentTarget : null;
        if (trigger) trigger.classList.add('active');
        if (name === 'stats') loadStats();
        if (name === 'icons') renderIcons();
        if (name === 'files') loadFileBrowser(browserState.path);
    }

    function showToast(msg, type) {
        const t = document.getElementById('toast');
        t.textContent = msg;
        t.className = 'toast ' + type;
        t.style.display = 'block';
        setTimeout(() => t.style.display = 'none', 3000);
    }

    function checkPasswordStrength(pw) {
        let score = 0;
        if (pw.length >= 6) score++;
        if (pw.length >= 10) score++;
        if (/[A-Z]/.test(pw)) score++;
        if (/[0-9]/.test(pw)) score++;
        if (/[^A-Za-z0-9]/.test(pw)) score++;
        const bar = document.getElementById('pwStrengthBar');
        const pct = Math.min(score * 20, 100);
        const colors = ['#dc2626','#f59e0b','#eab308','#22c55e','#16a34a'];
        bar.style.width = pct + '%';
        bar.style.background = colors[Math.min(score-1, 4)] || '#e5e7eb';
    }

    function escapeHtml(text) {
        return String(text || '')
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    function formatFileSize(bytes) {
        if (bytes === null || bytes === undefined) return '-';
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let size = bytes;
        let unitIndex = 0;
        while (size >= 1024 && unitIndex < units.length - 1) {
            size /= 1024;
            unitIndex += 1;
        }
        return `${size.toFixed(size >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
    }

    function setSelectedFiles(files) {
        selectedUploadFiles = Array.from(files || []);
        const hint = document.getElementById('selectedFilesHint');
        if (selectedUploadFiles.length === 0) {
            hint.textContent = '未选择文件';
            return;
        }
        const names = selectedUploadFiles.map(file => file.name);
        hint.textContent = `已选择 ${selectedUploadFiles.length} 个文件：${names.join('，')}`;
    }

    function handleFileInputChange() {
        setSelectedFiles(document.getElementById('upload_files').files);
    }

    function clearSelectedFiles() {
        selectedUploadFiles = [];
        document.getElementById('upload_files').value = '';
        setSelectedFiles([]);
    }

    function setUploadProgress(visible, percent = 0, label = '准备上传...') {
        const wrap = document.getElementById('uploadProgressWrap');
        const bar = document.getElementById('uploadProgressBar');
        const percentLabel = document.getElementById('uploadProgressPercent');
        const textLabel = document.getElementById('uploadProgressLabel');
        wrap.style.display = visible ? 'block' : 'none';
        bar.style.width = `${percent}%`;
        percentLabel.textContent = `${percent}%`;
        textLabel.textContent = label;
    }

    function resetUploadResult() {
        const resultBox = document.getElementById('uploadResultBox');
        resultBox.style.display = 'none';
        resultBox.textContent = '';
    }

    function encodeDataValue(value) {
        return encodeURIComponent(String(value || ''));
    }

    function decodeDataValue(value) {
        return decodeURIComponent(String(value || ''));
    }

    async function loadFileBrowser(path = '') {
        const targetPath = path || '';
        const resp = await fetch(`/api/files?path=${encodeURIComponent(targetPath)}`);
        if (resp.status === 401) { window.location.href = '/login'; return; }
        const result = await resp.json();
        if (!resp.ok || result.success === false) {
            showToast(result.error || '目录加载失败', 'error');
            return;
        }

        browserState.path = result.current_path || '';
        browserState.parentPath = result.parent_path || '';
        document.getElementById('currentPathLabel').textContent = `当前目录：${result.current_label}`;
        document.getElementById('upload_target_path').value = result.current_label;
        document.getElementById('mkdir_parent_path').value = result.current_label;
        document.getElementById('browserSummary').textContent = `当前目录共 ${result.entry_count} 项，上传后将自动刷新当前目录及上层目录页面。`;
        document.getElementById('openSiteDir').href = result.current_path ? `/${result.current_path}/` : '/';

        const body = document.getElementById('fileBrowserBody');
        if (!result.entries || result.entries.length === 0) {
            body.innerHTML = '<tr><td colspan="5">当前目录为空</td></tr>';
            return;
        }

        body.innerHTML = result.entries.map(entry => {
            const icon = entry.type === 'dir' ? '📁' : '📄';
            const name = escapeHtml(entry.name);
            const typeLabel = entry.type === 'dir' ? '目录' : '文件';
            const encodedPath = encodeDataValue(entry.relative_path || '');
            const encodedTypeLabel = encodeDataValue(typeLabel);
            const encodedName = encodeDataValue(entry.name || '');
            const action = entry.type === 'dir'
                ? `<button type="button" class="file-link-btn" data-action="open-dir" data-path="${encodedPath}">${icon} ${name}</button>`
                : `<a href="/${encodeURIComponent(entry.relative_path).replace(/%2F/g, '/')}" target="_blank">${icon} ${name}</a>`;
            const deleteLabel = entry.type === 'dir' ? '删除目录' : '删除文件';
            const controls = `
                <div class="browser-actions">
                    ${entry.type === 'dir'
                        ? `<button type="button" class="btn btn-primary btn-sm" data-action="open-dir" data-path="${encodedPath}">进入</button>`
                        : `<a class="btn btn-primary btn-sm" href="/${encodeURIComponent(entry.relative_path).replace(/%2F/g, '/')}" target="_blank" style="text-decoration:none;">打开</a>`
                    }
                    <button type="button" class="btn btn-danger btn-sm" data-action="delete-entry" data-path="${encodedPath}" data-type="${encodedTypeLabel}" data-name="${encodedName}">${deleteLabel}</button>
                </div>
            `;

            return `
                <tr>
                    <td>${action}</td>
                    <td><span class="file-type-badge">${typeLabel}</span></td>
                    <td>${entry.type === 'dir' ? '-' : formatFileSize(entry.size)}</td>
                    <td>${escapeHtml(entry.modified_at)}</td>
                    <td>${controls}</td>
                </tr>
            `;
        }).join('');
    }

    function openParentDirectory() {
        loadFileBrowser(browserState.parentPath || '');
    }

    async function createDirectory() {
        const nameInput = document.getElementById('new_directory_name');
        const directoryName = nameInput.value.trim();
        if (!directoryName) {
            showToast('请输入目录名称', 'error');
            return;
        }

        const resp = await fetch('/api/mkdir', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({parent_path: browserState.path || '', name: directoryName})
        });
        if (resp.status === 401) { window.location.href = '/login'; return; }
        const result = await resp.json();
        if (!resp.ok || result.success === false) {
            showToast(result.error || '新建目录失败', 'error');
            return;
        }

        nameInput.value = '';
        showToast(result.message || '目录创建成功', 'success');
        await loadFileBrowser(browserState.path);
    }

    async function deleteEntry(relativePath, typeLabel, name) {
        const confirmText = `确认删除${typeLabel}“${name}”吗？此操作不可恢复。`;
        if (!window.confirm(confirmText)) {
            return;
        }

        const resp = await fetch('/api/delete', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({path: relativePath})
        });
        if (resp.status === 401) { window.location.href = '/login'; return; }
        const result = await resp.json();
        if (!resp.ok || result.success === false) {
            showToast(result.error || '删除失败', 'error');
            return;
        }

        showToast(result.message || '删除成功', 'success');
        await loadFileBrowser(browserState.path);
    }

    async function uploadFiles() {
        if (!selectedUploadFiles || selectedUploadFiles.length === 0) {
            showToast('请先选择要上传的文件', 'error');
            return;
        }

        resetUploadResult();
        setUploadProgress(true, 0, '准备上传...');
        const formData = new FormData();
        formData.append('target_path', browserState.path || '');
        formData.append('overwrite', document.getElementById('upload_overwrite').value);
        selectedUploadFiles.forEach(file => formData.append('files', file));

        try {
            const result = await new Promise((resolve, reject) => {
                const xhr = new XMLHttpRequest();
                xhr.open('POST', '/api/upload');
                xhr.responseType = 'json';

                xhr.upload.onprogress = (event) => {
                    if (!event.lengthComputable) return;
                    const percent = Math.min(100, Math.round((event.loaded / event.total) * 100));
                    setUploadProgress(true, percent, `正在上传 ${selectedUploadFiles.length} 个文件...`);
                };

                xhr.onload = () => {
                    if (xhr.status === 401) {
                        window.location.href = '/login';
                        return;
                    }
                    const data = xhr.response || JSON.parse(xhr.responseText || '{}');
                    if (xhr.status >= 200 && xhr.status < 300 && data.success !== false) {
                        resolve(data);
                    } else {
                        reject(new Error(data.error || '上传失败'));
                    }
                };

                xhr.onerror = () => reject(new Error('网络错误，上传失败'));
                xhr.send(formData);
            });

            const summaryLines = [];
            summaryLines.push(`已上传 ${result.saved.length} 个文件`);
            summaryLines.push(`自动生成 ${result.generated_count} 个目录页面`);
            if (result.skipped && result.skipped.length) {
                summaryLines.push(`跳过：${result.skipped.join('，')}`);
            }

            const resultBox = document.getElementById('uploadResultBox');
            resultBox.style.display = 'block';
            resultBox.textContent = summaryLines.join('\\n');

            setUploadProgress(true, 100, '上传完成');
            showToast(result.message || '上传完成', 'success');
            clearSelectedFiles();
            await loadFileBrowser(browserState.path);
        } catch (error) {
            setUploadProgress(false);
            showToast(error.message || '上传失败', 'error');
        }
    }

    async function loadConfig() {
        const resp = await fetch('/api/config');
        if (resp.status === 401) { window.location.href = '/login'; return; }
        config = await resp.json();
        document.getElementById('site_title').value = config.site?.title || '';
        document.getElementById('site_url').value = config.site?.url || '';
        document.getElementById('intro_title').value = config.intro?.title || '';
        document.getElementById('intro_content').value = config.intro?.content || '';
        document.getElementById('intro_enabled').value = String(config.intro?.enabled ?? true);
        document.getElementById('footer_copyright').value = config.footer?.copyright || '';
    }

    async function saveConfig() {
        config.site = config.site || {};
        config.site.title = document.getElementById('site_title').value;
        config.site.url = document.getElementById('site_url').value;
        config.intro = config.intro || {};
        config.intro.title = document.getElementById('intro_title').value;
        config.intro.content = document.getElementById('intro_content').value;
        config.intro.enabled = document.getElementById('intro_enabled').value === 'true';
        config.footer = config.footer || {};
        config.footer.copyright = document.getElementById('footer_copyright').value;
        const resp = await fetch('/api/config', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(config)
        });
        if (resp.status === 401) { window.location.href = '/login'; return; }
        if (resp.ok) showToast('配置已保存', 'success');
        else showToast('保存失败', 'error');
    }

    function renderIcons() {
        const icons = config.file_icons || {};
        const list = document.getElementById('iconList');
        list.innerHTML = '';
        const emojiMap = {
            'folder':'📁','disc':'💿','archive':'📦','pdf':'📕','doc':'📘','xls':'📗',
            'ppt':'📙','code':'💻','binary':'⚙️','package':'📦','executable':'⚡',
            'image':'🖼️','text':'📝','data':'📊','model':'🧠','video':'🎬','build':'🔧',
            '3d':'🧊','file':'📄'
        };
        for (const [ext, info] of Object.entries(icons)) {
            if (ext === 'default' || ext === 'folder') continue;
            const icon = info.icon || 'file';
            const emoji = emojiMap[icon] || '📄';
            list.innerHTML += `<div class="icon-item"><span class="ext">${ext}</span><span class="icon-emoji">${emoji}</span><span>${icon}</span></div>`;
        }
    }

    async function addIconMapping() {
        const ext = document.getElementById('new_ext').value.trim();
        const icon = document.getElementById('new_icon').value;
        if (!ext) { showToast('请输入扩展名', 'error'); return; }
        config.file_icons = config.file_icons || {};
        config.file_icons[ext] = { icon: icon, color: '#6b7280' };
        const resp = await fetch('/api/config', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(config)
        });
        if (resp.ok) { showToast('映射已添加', 'success'); renderIcons(); }
        else showToast('添加失败', 'error');
    }

    async function loadStats() {
        const resp = await fetch('/api/stats');
        if (resp.status === 401) { window.location.href = '/login'; return; }
        const stats = await resp.json();
        const grid = document.getElementById('statsGrid');
        grid.innerHTML = `
            <div class="stat-card"><div class="stat-value">${stats.total_visits || 0}</div><div class="stat-label">总访问量</div></div>
            <div class="stat-card"><div class="stat-value">${Object.keys(stats.daily || {}).length}</div><div class="stat-label">统计天数</div></div>
            <div class="stat-card"><div class="stat-value">${Object.keys(stats.pages || {}).length}</div><div class="stat-label">访问页面数</div></div>
            <div class="stat-card"><div class="stat-value">${Object.keys(stats.downloads || {}).length}</div><div class="stat-label">下载文件数</div></div>
        `;
        const topPages = document.getElementById('topPages');
        const pages = Object.entries(stats.pages || {}).sort((a,b) => b[1]-a[1]).slice(0,10);
        topPages.innerHTML = pages.map(([p,c]) => `<li><span>${p}</span><span class="count">${c}</span></li>`).join('');
        const topDL = document.getElementById('topDownloads');
        const dls = Object.entries(stats.downloads || {}).sort((a,b) => b[1]-a[1]).slice(0,10);
        topDL.innerHTML = dls.map(([f,c]) => `<li><span>${f}</span><span class="count">${c}</span></li>`).join('');
    }

    async function regenerateAll() {
        showToast('正在生成...', 'success');
        const resp = await fetch('/api/generate', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({recursive: true})
        });
        const result = await resp.json();
        if (result.success) showToast(`生成完成: ${result.count} 个目录`, 'success');
        else showToast('生成失败: ' + (result.error || ''), 'error');
    }

    async function regenerateRoot() {
        const resp = await fetch('/api/generate', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({recursive: false})
        });
        const result = await resp.json();
        if (result.success) showToast('首页已生成', 'success');
        else showToast('生成失败', 'error');
    }

    async function changePassword() {
        const current = document.getElementById('current_password').value;
        const newPw = document.getElementById('new_password').value;
        const confirm = document.getElementById('confirm_password').value;
        if (!current || !newPw || !confirm) { showToast('请填写所有字段', 'error'); return; }
        if (newPw.length < 6) { showToast('新密码至少6位', 'error'); return; }
        if (newPw !== confirm) { showToast('两次密码不一致', 'error'); return; }
        const resp = await fetch('/api/change-password', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({current_password: current, new_password: newPw})
        });
        const result = await resp.json();
        if (result.success) {
            showToast('密码修改成功，请重新登录', 'success');
            setTimeout(() => window.location.href = '/logout', 1500);
        } else {
            showToast(result.error || '修改失败', 'error');
        }
    }

    loadConfig();
    loadFileBrowser('');

    document.getElementById('fileBrowserBody').addEventListener('click', (event) => {
        const target = event.target.closest('[data-action]');
        if (!target) return;

        const action = target.dataset.action || '';
        if (action === 'open-dir') {
            loadFileBrowser(decodeDataValue(target.dataset.path));
            return;
        }

        if (action === 'delete-entry') {
            deleteEntry(
                decodeDataValue(target.dataset.path),
                decodeDataValue(target.dataset.type),
                decodeDataValue(target.dataset.name)
            );
        }
    });

    const uploadDropZone = document.getElementById('uploadDropZone');
    ['dragenter', 'dragover'].forEach(eventName => {
        uploadDropZone.addEventListener(eventName, (event) => {
            event.preventDefault();
            event.stopPropagation();
            uploadDropZone.classList.add('dragover');
        });
    });
    ['dragleave', 'drop'].forEach(eventName => {
        uploadDropZone.addEventListener(eventName, (event) => {
            event.preventDefault();
            event.stopPropagation();
            uploadDropZone.classList.remove('dragover');
        });
    });
    uploadDropZone.addEventListener('drop', (event) => {
        const droppedFiles = event.dataTransfer && event.dataTransfer.files ? event.dataTransfer.files : [];
        setSelectedFiles(droppedFiles);
    });
    </script>
</body>
</html>
'''


@app.route('/')
def index():
    record_visit("/")
    return send_from_directory(BASE_DIR, 'index.html')


@app.route('/login', methods=['GET', 'POST'])
def login():
    if request.method == 'GET':
        if session.get('logged_in'):
            return redirect(url_for('admin'))
        return render_template_string(LOGIN_HTML, error=None)

    username = request.form.get('username', '')
    password = request.form.get('password', '')

    auth = load_auth_config()
    stored_hash = auth.get('password_hash', '')
    input_hash = hash_password(password)

    if username == auth.get('username', 'admin') and input_hash == stored_hash:
        session['logged_in'] = True
        session['username'] = username
        session.permanent = True
        return redirect(url_for('admin'))
    else:
        return render_template_string(LOGIN_HTML, error='用户名或密码错误')


@app.route('/logout')
def logout():
    session.clear()
    return redirect(url_for('login'))


@app.route('/admin')
@login_required
def admin():
    username = session.get('username', 'admin')
    return render_template_string(ADMIN_HTML, username=username)


@app.route('/api/config', methods=['GET'])
def get_config():
    return jsonify(load_config())


@app.route('/api/config', methods=['POST'])
@login_required
def update_config():
    config = request.json
    save_config(config)
    return jsonify({"success": True})


@app.route('/api/stats', methods=['GET'])
@login_required
def get_stats():
    return jsonify(load_stats())


@app.route('/api/files', methods=['GET'])
@login_required
def list_files():
    requested_path = request.args.get('path', '')
    try:
        directory_path = resolve_relative_directory(requested_path)
    except ValueError as exc:
        return jsonify({"success": False, "error": str(exc)}), 400

    if not directory_path.exists() or not directory_path.is_dir():
        return jsonify({"success": False, "error": "目录不存在"}), 404

    return jsonify(get_directory_payload(directory_path))


@app.route('/api/mkdir', methods=['POST'])
@login_required
def make_directory():
    data = request.json or {}
    parent_path = data.get('parent_path', '')
    directory_name = sanitize_directory_name(data.get('name', ''))

    if not directory_name:
        return jsonify({"success": False, "error": "目录名称非法"}), 400

    try:
        parent_directory = resolve_relative_directory(parent_path)
    except ValueError as exc:
        return jsonify({"success": False, "error": str(exc)}), 400

    if not parent_directory.exists() or not parent_directory.is_dir():
        return jsonify({"success": False, "error": "父目录不存在"}), 404

    target_directory = parent_directory / directory_name
    if target_directory.exists():
        return jsonify({"success": False, "error": "目录已存在"}), 400

    target_directory.mkdir(parents=False, exist_ok=False)
    generated_count = regenerate_directory_chain(target_directory)

    return jsonify({
        "success": True,
        "message": f"目录“{directory_name}”已创建",
        "path": to_relative_path(target_directory),
        "generated_count": generated_count,
    })


@app.route('/api/upload', methods=['POST'])
@login_required
def upload_files():
    target_path = request.form.get('target_path', '')
    overwrite = request.form.get('overwrite', 'false').lower() == 'true'
    files = request.files.getlist('files')

    if not files:
        return jsonify({"success": False, "error": "未选择上传文件"}), 400

    try:
        directory_path = resolve_relative_directory(target_path)
    except ValueError as exc:
        return jsonify({"success": False, "error": str(exc)}), 400

    if not directory_path.exists() or not directory_path.is_dir():
        return jsonify({"success": False, "error": "上传目标目录不存在"}), 404

    saved = []
    skipped = []

    for storage in files:
        filename = sanitize_upload_filename(storage.filename)
        if not filename:
            skipped.append(storage.filename or "未命名文件")
            continue

        file_path = directory_path / filename
        if file_path.exists() and not overwrite:
            skipped.append(filename)
            continue

        storage.save(file_path)
        saved.append(to_relative_path(file_path))

    if not saved:
        return jsonify({"success": False, "error": "没有可保存的文件，请检查是否同名冲突"}), 400

    generated_count = regenerate_directory_chain(directory_path)
    message = f"上传完成，已保存 {len(saved)} 个文件"
    if skipped:
        message += f"，跳过 {len(skipped)} 个同名或非法文件"

    return jsonify({
        "success": True,
        "message": message,
        "saved": saved,
        "skipped": skipped,
        "generated_count": generated_count,
        "current_path": to_relative_path(directory_path),
    })


@app.route('/api/delete', methods=['POST'])
@login_required
def delete_entry():
    data = request.json or {}
    relative_path = data.get('path', '')

    try:
        target_path = resolve_relative_path(relative_path)
    except ValueError as exc:
        return jsonify({"success": False, "error": str(exc)}), 400

    if target_path == BASE_DIR:
        return jsonify({"success": False, "error": "不能删除站点根目录"}), 400

    if not target_path.exists():
        return jsonify({"success": False, "error": "目标不存在"}), 404

    parent_directory = target_path.parent
    removed_type = "目录" if target_path.is_dir() else "文件"
    removed_name = target_path.name

    if target_path.is_dir():
        shutil.rmtree(target_path)
    else:
        target_path.unlink()

    generated_count = regenerate_directory_chain(parent_directory)

    return jsonify({
        "success": True,
        "message": f"{removed_type}“{removed_name}”已删除",
        "generated_count": generated_count,
    })


@app.route('/api/visit', methods=['POST'])
def track_visit():
    data = request.json or {}
    page = data.get('page', '/')
    record_visit(page)
    return jsonify({"success": True})


@app.route('/api/download', methods=['POST'])
def track_download():
    data = request.json or {}
    filename = data.get('filename', '')
    if filename:
        record_download(filename)
    return jsonify({"success": True})


@app.route('/api/generate', methods=['POST'])
@login_required
def generate():
    data = request.json or {}
    recursive = data.get('recursive', False)
    try:
        cmd = [sys.executable, str(BASE_DIR / "generate_directory.py"), str(BASE_DIR)]
        if recursive:
            cmd.append('-r')
        result = subprocess.run(cmd, capture_output=True, text=True, cwd=str(BASE_DIR))
        count = result.stdout.count('已生成:')
        return jsonify({"success": True, "count": count, "output": result.stdout[-500:]})
    except Exception as e:
        return jsonify({"success": False, "error": str(e)})


@app.route('/api/change-password', methods=['POST'])
@login_required
def change_password():
    data = request.json or {}
    current_password = data.get('current_password', '')
    new_password = data.get('new_password', '')

    if not current_password or not new_password:
        return jsonify({"success": False, "error": "请填写所有字段"})

    if len(new_password) < 6:
        return jsonify({"success": False, "error": "新密码至少6位"})

    auth = load_auth_config()
    if hash_password(current_password) != auth.get('password_hash', ''):
        return jsonify({"success": False, "error": "当前密码错误"})

    auth['password_hash'] = hash_password(new_password)
    save_auth_config(auth)
    return jsonify({"success": True})


@app.route('/<path:path>')
def serve_file(path):
    if path.endswith('/'):
        record_visit("/" + path)
    file_path = BASE_DIR / path
    if file_path.is_dir():
        index_path = file_path / 'index.html'
        if index_path.exists():
            return send_from_directory(file_path, 'index.html')
    return send_from_directory(BASE_DIR, path)


if __name__ == '__main__':
    auth = load_auth_config()
    host = os.environ.get("HOST", "0.0.0.0")
    port = int(os.environ.get("PORT", "5000"))
    debug = os.environ.get("DEBUG", "false").lower() in {"1", "true", "yes", "on"}
    print("=" * 50)
    print("  百问科技下载站 - 管理后台")
    print(f"  站点地址: http://{host}:{port}")
    print(f"  管理后台: http://{host}:{port}/admin")
    print(f"  管理员账号: {auth.get('username', 'admin')}")
    print("  默认密码: admin123（请及时修改）")
    print("=" * 50)
    app.run(host=host, port=port, debug=debug)
