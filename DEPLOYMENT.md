# 部署说明

本文档说明如何把本仓库部署为可用的下载站后台。

默认示例环境：

- 域名：`dl.100ask.net`
- 服务器目录：`/home1/dlfile`
- 后台监听：`127.0.0.1:5000`

## 1. 上传代码

把本仓库代码上传到服务器目录，例如：

```bash
/home1/dlfile
```

然后再把你自己的资源目录上传进去，例如：

- `Hardware/`
- `Video/`
- `Tools/`

最终建议目录结构如下：

```text
/home1/dlfile/
├── admin_server.py
├── generate_directory.py
├── directory-template.html
├── config.json
├── requirements.txt
├── gunicorn.conf.py
├── wsgi.py
├── .venv/
├── Hardware/
├── Video/
├── Tools/
├── index.html
├── access_stats.json
└── admin_auth.json
```

## 2. 临时启动方式

只用于调试：

```bash
cd /home1/dlfile
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
python3 generate_directory.py . -r
python3 admin_server.py
```

## 3. 生产环境推荐方式

生产环境不要长期直接运行：

```bash
python3 admin_server.py
```

推荐使用：

- `Gunicorn`
- `systemd`
- `Nginx`

### 一键可复制脚本

```bash
set -e

APP_DIR="/home1/dlfile"
VENV_DIR="$APP_DIR/.venv"
SERVICE_NAME="dl-download-site"

cd "$APP_DIR"
test -f admin_server.py || { echo "错误：admin_server.py 不在 $APP_DIR"; exit 1; }

python3 -m venv "$VENV_DIR"
source "$VENV_DIR/bin/activate"
pip install -U pip setuptools wheel

if [ -f "$APP_DIR/requirements.txt" ]; then
    pip install -r "$APP_DIR/requirements.txt"
else
    pip install flask gunicorn
fi

cat > "$APP_DIR/wsgi.py" <<'EOF'
from admin_server import app
EOF

cat > "$APP_DIR/gunicorn.conf.py" <<'EOF'
bind = "127.0.0.1:5000"
workers = 2
threads = 4
timeout = 600
accesslog = "-"
errorlog = "-"
capture_output = True
EOF

cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=dl.100ask.net admin server
After=network.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=$APP_DIR
Environment=HOST=127.0.0.1
Environment=PORT=5000
Environment=DEBUG=false
ExecStart=$VENV_DIR/bin/gunicorn -c $APP_DIR/gunicorn.conf.py wsgi:app
Restart=always
RestartSec=5
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
EOF

pkill -f "python3 admin_server.py" || true
pkill -f "gunicorn.*wsgi:app" || true

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
systemctl status "$SERVICE_NAME" --no-pager
```

### 常用命令

```bash
systemctl start dl-download-site
systemctl stop dl-download-site
systemctl restart dl-download-site
systemctl status dl-download-site
journalctl -u dl-download-site -f
```

## 4. Nginx 配置

Nginx 需要反代到：

```text
http://127.0.0.1:5000
```

核心配置：

```nginx
location / {
    proxy_pass http://127.0.0.1:5000;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Connection "";
    proxy_read_timeout 600s;
    proxy_send_timeout 600s;
    send_timeout 600s;
    client_max_body_size 20G;
    proxy_buffering off;
}
```

完整示例：

- `deploy/nginx/dl-download-site.conf`
- `deploy/nginx/dl.100ask.net.baota.conf`

## 5. 宝塔说明

如果你使用宝塔：

1. 不要使用 `fancyindex`
2. 不要保留旧的 PHP 配置
3. 不要保留旧的 `rewrite` 伪静态配置
4. 所有请求统一反代到 `127.0.0.1:5000`

同时，宝塔会校验 SSL 注释区块，这两行必须保留：

- `#SSL-START SSL相关配置，请勿删除或修改下一行带注释的404规则`
- `#error_page 404/404.html;`

详细说明：

- [宝塔Nginx面板部署说明](./deploy/%E5%AE%9D%E5%A1%94Nginx%E9%9D%A2%E6%9D%BF%E9%83%A8%E7%BD%B2%E8%AF%B4%E6%98%8E.md)

## 6. 上线后检查

建议逐项确认：

1. `https://你的域名/` 可访问
2. `https://你的域名/admin` 可登录
3. 后台“文件管理”可以进入目录
4. 上传文件后前台会自动显示
5. 删除文件或目录后前台会同步刷新
6. `systemctl status dl-download-site` 正常
7. `nginx -t` 正常
