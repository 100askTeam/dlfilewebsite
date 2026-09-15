# dlfilewebsite

`dlfilewebsite` 是 `dl.100ask.net` 的 Go 下载站与签名发布中心。生产环境只需要两个静态
编译的二进制：

- `dladmin-go`：公开下载、人工后台、验签、暂存、审批发布和更新查询；
- `dlctl`：给受限 SSH/SCP 与 CI 使用的服务器端验签/发布工具。

## 已实现

- bcrypt 管理员密码、SQLite 哈希会话、Secure/HttpOnly/SameSite Cookie；
- CSRF、同源检查、登录限速、安全响应头和角色检查；
- 文件上传、新建目录、可恢复删除和审计日志；
- 私有 `incoming`、签名清单校验、二次验签、不可变版本目录；
- 人工后台上传，或 GitHub Actions + SSH/SCP 自动导入；
- GitHub Runner 验签中转并原子恢复数据库中已有但公开文件丢失的版本；
- `product` 到 minisign 公钥的一对一绑定，不同软件不能互用发布签名；
- 任意已注册产品的精确版本增量选择，不能增量时强制返回完整安装包；
- Tauri 更新请求可用 `X-Update-Mode: full` 显式取得签名完整包恢复路径；
- `dl.100ask.net` 主下载地址及最多四个 HTTPS 镜像地址。

Python 版文件只为旧部署迁移保留，新部署不要安装 Flask/Gunicorn。
本仓库 CI 只执行 Go 测试、vet 和静态构建，不会自动重启服务器，也不会改动
`/home1/dlfile` 内的现有下载文件。

## 本地验证

需要 Go 1.21 或更高版本：

```bash
go test ./...
go vet ./...
CGO_ENABLED=0 go build -o build/dladmin-go .
CGO_ENABLED=0 go build -o build/dlctl ./cmd/dlctl
```

## 生产目录

```text
/home1/
├── dladmin-code/current/   # Go 源码和 build/dladmin-go、build/dlctl
├── dlfile/                 # 现有下载文件，不随源码发布覆盖
│   └── Tools/
│       ├── lynx/stable/0.9.1/...
│       └── usbtoolbox/stable/1.0.1/...
└── dlfile-state/           # Nginx 永不公开
    ├── dladmin.db
    ├── incoming/
    ├── staged/
    └── trash/
```

`/home1/dlfile` 与 `/home1/dlfile-state` 必须位于同一文件系统，以保证暂存、发布和回收操作使用原子重命名。

## 首次启动

```bash
export HOST=127.0.0.1
export PORT=5001
export DL_PUBLIC_DIR=/home1/dlfile
export DL_STATE_DIR=/home1/dlfile-state
export DL_ADMIN_USERNAME=admin
export DL_ADMIN_PASSWORD='replace-with-a-unique-password'
export DL_RELEASE_PUBLIC_KEYS_DIR=/etc/dladmin/release-keys
/home1/dladmin-code/current/build/dladmin-go
```

程序没有默认密码。首次成功启动并写入管理员后，从环境文件中删除
`DL_ADMIN_PASSWORD`。公钥目录以 `<product>.pub` 命名，例如 `lynx.pub` 和
`usbtoolbox.pub`；签名私钥只保存在发布机/GitHub Secrets，下载服务器只保存公钥。

完整上线流程见 [DEPLOYMENT.md](DEPLOYMENT.md)，发布清单格式见
[docs/RELEASE_FORMAT.md](docs/RELEASE_FORMAT.md)，跨产品目录与流水线规范见
[docs/PRODUCT_RELEASE_STANDARD.md](docs/PRODUCT_RELEASE_STANDARD.md)，整体设计见
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)。
