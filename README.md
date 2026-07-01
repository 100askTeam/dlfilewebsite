# dlfilewebsite

`dlfilewebsite` 是一个下载站代码仓库，提供：

- 静态目录页生成
- 后台登录与配置管理
- 后台目录浏览
- 后台上传文件
- 新建目录、删除文件和删除目录
- 上传后自动刷新目录展示
- 访问统计
- `Nginx + Gunicorn + systemd` 的 Python 部署方案
- `Go + systemd + Nginx` 的单二进制部署方案
- 宝塔面板部署方案

这个仓库只保存代码和部署文件，不包含实际资源文件。

## 当前推荐

当前更推荐直接使用 Go 版：

- 单文件运行
- 不依赖 Python / Flask / Gunicorn
- 支持后台登录、上传、遍历、新建目录、删除、配置编辑、访问统计
- 可使用 `CGO_ENABLED=0` 生成 Linux 静态二进制

Go 版入口文件：

- `main.go`
- `go.mod`
- `web/`

## 仓库内容

```text
.
├── go.mod
├── main.go
├── web/
│   ├── login.html
│   ├── admin.html
│   └── directory.html
├── GO部署说明.md
├── dladmin-go.service
├── admin_server.py
├── generate_directory.py
├── directory-template.html
├── config.json
├── requirements.txt
├── wsgi.py
├── gunicorn.conf.py
├── .env.example
├── start.bat
├── deploy/
│   ├── nginx/
│   ├── systemd/
│   ├── 最终上线配置说明.md
│   └── 宝塔Nginx面板部署说明.md
├── scripts/
│   ├── start.sh
│   └── rebuild.sh
└── DEPLOYMENT.md
```

## 不包含的内容

以下内容不建议提交到本仓库：

- `Hardware/`
- `Video/`
- `Tools/`
- 其他大体积资源目录
- 自动生成的 `index.html`
- 运行时生成的 `admin_auth.json`
- 运行时生成的 `access_stats.json`
- 虚拟环境 `.venv/`

## 已完成功能

当前版本已经支持：

1. 后台登录
2. 后台修改站点配置
3. 后台目录浏览
4. 后台新建目录
5. 后台上传文件
6. 拖拽上传和上传进度显示
7. 后台删除文件和目录
8. 上传、新建、删除后自动刷新目录页
9. 访问统计查看
10. 后台修改密码

## 文件过滤规则

前台展示和后台文件管理都会自动隐藏以下文件：

- `.py`
- `.json`
- `.txt`
- `.html`
- 所有以下划线 `_` 开头的文件

这些文件仍会保留在服务器中，只是不显示在列表里。

## 快速开始

### Go 版编译 Linux 单文件

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o dladmin-go .
```

### Linux 直接运行 Go 版

```bash
chmod +x dladmin-go
./dladmin-go
```

默认监听：

```text
0.0.0.0:5000
```

可用环境变量覆盖：

```bash
HOST=127.0.0.1 PORT=5000 ./dladmin-go
```

### Python 版

#### Linux

```bash
cd /home1/dlfile
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
python3 generate_directory.py . -r
python3 admin_server.py
```

#### Windows

```bat
start.bat
```

## 推荐部署

生产环境推荐使用：

- Go 版：`dladmin-go + systemd + Nginx`
- Python 版：`Gunicorn + systemd + Nginx`
- `systemd`
- `Nginx`

详细说明见：

- [DEPLOYMENT.md](./DEPLOYMENT.md)
- [GO部署说明.md](./GO%E9%83%A8%E7%BD%B2%E8%AF%B4%E6%98%8E.md)
- [最终上线配置说明](./deploy/%E6%9C%80%E7%BB%88%E4%B8%8A%E7%BA%BF%E9%85%8D%E7%BD%AE%E8%AF%B4%E6%98%8E.md)
- [宝塔 Nginx 面板部署说明](./deploy/%E5%AE%9D%E5%A1%94Nginx%E9%9D%A2%E6%9D%BF%E9%83%A8%E7%BD%B2%E8%AF%B4%E6%98%8E.md)

## 常用命令

### 重新生成目录页

```bash
bash scripts/rebuild.sh
```

### 查看 Go 服务状态

```bash
systemctl status dladmin-go
```

### 查看 Python 服务状态

```bash
systemctl status dl-download-site
```

### 查看服务日志

```bash
journalctl -u dladmin-go -f
journalctl -u dl-download-site -f
```

## 默认后台信息

- 后台地址：`https://你的域名/admin`
- 默认账号：`admin`
- 默认密码：`admin123`

首次部署后建议立刻修改后台密码。
