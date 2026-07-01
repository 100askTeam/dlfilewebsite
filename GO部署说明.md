# dladmin-go 部署说明

`dladmin-go` 是当前下载站的 Go 单二进制版本，目标是：

- 不再依赖 Python
- 不再依赖 Flask / Gunicorn
- 直接一个二进制运行
- 支持后台登录、目录遍历、上传、新建目录、删除、配置编辑、访问统计

## 已生成的二进制

当前目录已经编译出 Linux `amd64` 版本：

```text
i:\dlfile\dladmin-go
```

这是通过下面命令生成的：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dladmin-go .
```

## 服务器目录建议

```text
/home1/dlfile/
├── dladmin-go
├── config.json
├── admin_auth.json
├── access_stats.json
├── Hardware/
├── Video/
├── Tools/
└── .well-known/
```

说明：

- `Hardware/`、`Video/`、`Tools/` 是你的资源目录
- `config.json` 继续沿用现有配置
- `admin_auth.json` 和 `access_stats.json` 可沿用现有文件
- `.well-known/` 用于证书校验时仍可保留

## Linux 上直接运行

上传到 Linux 服务器后：

```bash
cd /home1/dlfile
chmod +x dladmin-go
./dladmin-go
```

默认监听：

```text
0.0.0.0:5000
```

可通过环境变量修改：

```bash
HOST=127.0.0.1 PORT=5000 ./dladmin-go
```

## Nginx 反向代理

Go 版仍然建议通过 Nginx 对外提供服务，代理到：

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

## systemd 常驻运行

参考：

```text
dladmin-go.service
```

部署步骤：

```bash
cp /home1/dlfile/dladmin-go.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable dladmin-go
systemctl restart dladmin-go
systemctl status dladmin-go
```

## 常用命令

```bash
systemctl start dladmin-go
systemctl stop dladmin-go
systemctl restart dladmin-go
systemctl status dladmin-go
journalctl -u dladmin-go -f
```

## 当前 Go 版说明

当前 Go 版采用“动态目录展示”方式：

- 不再依赖 `generate_directory.py`
- 目录访问时由 Go 动态渲染页面
- 上传、新建、删除后无需重新生成静态页

也就是说，Go 版核心优势是：

- 部署简单
- 单文件运行
- 不再维护 Python 环境

## 默认后台信息

- 后台地址：`https://你的域名/admin`
- 默认账号：`admin`
- 默认密码：`admin123`

首次上线后建议立即修改密码。
