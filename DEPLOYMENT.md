# dl.100ask.net 上线与迁移

## 1. 上线前

1. 备份当前下载目录、Nginx 配置和旧后台状态。
2. 立即从公开根目录移走 `shell.sh`、部署脚本、源码和密钥。
3. 保留现有 `/home1/dlfile`，建立私有 `/home1/dlfile-state`，两者必须同盘。
4. 源码只同步到 `/home1/dladmin-code/current`，不得把源码、密钥、日志或数据库复制到 `/home1/dlfile`。
5. 按 [GO部署说明.md](GO部署说明.md) 安装当前 Go 服务并配置发布公钥。

## 2. Nginx

安装 `deploy/nginx/dl-download-site.conf`，确认 TLS 证书路径后执行：

```bash
sudo nginx -t
sudo systemctl reload nginx
```

Nginx 只代理到 `127.0.0.1:5001`。不要再启用 fancyindex、PHP、旧 Flask/Gunicorn
后台或第二套能直接写公开目录的上传接口。

## 3. 人工发布

后台地址为 `/admin`。在“版本发布”页同时选择 `release-set.json` 和全部资产。服务器完成
结构、版本、大小、SHA-256 和 minisign 验证后只会标记为 `staged`；人工点击“确认发布”
后才原子移动到公开的不可变目录。

正式资产固定发布到 `/Tools/<product>/releases/<channel>/<version>/`。普通“文件管理”
不能覆盖或删除任何产品的 `releases/` 子树，避免绕过验签；`/Tools/<product>/` 下其他
人工资料仍可照常管理。

旧版曾把正式资产写到根级 `/releases/<product>/...`。升级二进制后先保持服务运行，执行
迁移预检（不搬文件、不更新发布路径，只追加一条审计记录）：

```bash
DL_STATE_DIR=/home1/dlfile-state \
DL_PUBLIC_DIR=/home1/dlfile \
DL_RELEASE_PUBLIC_KEY_FILE=/etc/dladmin/release.pub \
DL_RELEASE_ACTOR=admin \
/home1/dladmin-code/current/build/dlctl migrate-tools-layout --dry-run
```

确认计划只包含预期产品和版本后停站，再执行实际迁移：

```bash
DL_STATE_DIR=/home1/dlfile-state \
DL_PUBLIC_DIR=/home1/dlfile \
DL_RELEASE_PUBLIC_KEY_FILE=/etc/dladmin/release.pub \
DL_RELEASE_ACTOR=admin \
/home1/dladmin-code/current/build/dlctl migrate-tools-layout
```

命令会先重新验签，再逐版本原子移动文件并同步 SQLite 路径；重复执行返回 `count: 0`。
迁移后根级 `/releases` 不再存文件，只由 Nginx/Go 返回到 `/Tools/...` 的 308 兼容跳转。

## 4. SSH/SCP 与 GitHub Actions

服务器端先验证一个发布集：

```bash
DL_STATE_DIR=/home1/dlfile-state \
DL_PUBLIC_DIR=/home1/dlfile \
DL_RELEASE_PUBLIC_KEY_FILE=/etc/dladmin/release.pub \
DL_RELEASE_ACTOR=admin \
dlctl import ci-job-id
```

审批后执行 `dlctl publish RELEASE_ID`。仓库内的 `scripts/publish-release.sh` 会先上传到
`.part-*`，完整传输后再重命名，服务端不会读取半包。参考工作流：
`docs/examples/publish-release.workflow.yml`。该文件不会在本仓库自动执行。

生产 SSH key 必须绑定受限命令；不要允许该 key 获得交互式 root shell。仓库提供的白名单包装器
只接受无副作用的协议探针、创建 `.part-*`、旧式 SCP sink、同名原子移动和
`dlctl import` 五类固定命令：

```bash
sudo install -o root -g root -m 0755 deploy/ssh/dl-release-command \
  /usr/local/libexec/dl-release-command
sudo install -d -o root -g root -m 0700 /root/.ssh
sudo sh -c 'printf "%s\n" \
  "restrict,command=\"/usr/local/libexec/dl-release-command\" ssh-ed25519 AAAA... lynx-release-ci" \
  >> /root/.ssh/authorized_keys'
sudo chmod 0600 /root/.ssh/authorized_keys
```

旧部署若把 key 绑定到源码目录下的脚本，必须改为稳定安装路径，避免源码同步与实际
forced-command 版本漂移：

```bash
sudo install -o root -g root -m 0755 deploy/ssh/dl-release-command \
  /usr/local/libexec/dl-release-command
sudo cp -a /root/.ssh/authorized_keys \
  "/root/.ssh/authorized_keys.before-release-command.$(date +%Y%m%d%H%M%S)"
sudo sed -i \
  's#command="/home1/dladmin-code/current/deploy/ssh/dl-release-command"#command="/usr/local/libexec/dl-release-command"#' \
  /root/.ssh/authorized_keys
sudo grep -F 'command="/usr/local/libexec/dl-release-command"' \
  /root/.ssh/authorized_keys
```

部署后，使用专用私钥从允许访问 SSH 的主机执行下列命令，只应输出固定协议版本；
它不会创建目录、导入文件或改变发布状态：

```bash
ssh root@dl.100ask.net release-channel-probe-v2
# dl-release-command protocol=2
```

v1 探针仍返回旧的 `lynx-release-command protocol=1`，只用于已发布 LYNX 工作流兼容；
新产品和新工作流必须使用产品无关的 v2。

当 CI 复用现有 root SSH 入口时，这枚专用 key 必须保留 `restrict,command=...`
限制，不得复用为交互登录 key。CI 使用 `scp -O`，forced-command 只开放可审计的
SCP sink 和 `dlctl import`，不允许 SFTP 或任意 shell。

自动发布默认关闭，只有明确设置 `DL_PUBLISH_NOW=true` 才会导入后立即发布。

## 5. 回归检查

```bash
go test ./...
go vet ./...
curl -fsS https://dl.100ask.net/
curl -fsS https://dl.100ask.net/api/v1/updates/lynx/stable/windows-x86_64/0.9.0
curl -fsSI https://dl.100ask.net/releases/lynx/stable/0.9.0/release-set.json
sudo systemctl status dladmin-go --no-pager
sudo journalctl -u dladmin-go -n 100 --no-pager
```

另外检查：错误密码限速、缺少 CSRF 的写请求被拒绝、非法签名无法暂存、旧版本无法覆盖
新 channel head、增量不存在时返回 full、下载后二次计算 SHA-256/验签成功。
