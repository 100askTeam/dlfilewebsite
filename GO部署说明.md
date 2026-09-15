# dladmin-go 部署说明

当前 `dl.100ask.net` 生产布局使用三个彼此分离的目录：

```bash
sudo install -d -o root -g root -m 0755 /home1/dladmin-code/current
sudo install -d -o root -g root -m 0755 /home1/dlfile
sudo install -d -o root -g root -m 0750 /home1/dlfile-state
sudo install -d -o root -g root -m 0750 /etc/dladmin
```

构建并安装：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o build/dladmin-go .
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o build/dlctl ./cmd/dlctl
sudo install -o root -g root -m 0755 build/dladmin-go build/dlctl /home1/dladmin-code/current/build/
sudo install -o root -g root -m 0640 release.pub /etc/dladmin/release.pub
sudo install -o root -g root -m 0644 deploy/systemd/dladmin-go.service /etc/systemd/system/
```

复制 `.env.example` 为 `/home1/dlfile-state/dladmin.env`，权限设为 `0600 root:root`。首次启动
时设置一个至少 12 位的唯一 `DL_ADMIN_PASSWORD`：

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now dladmin-go
sudo journalctl -u dladmin-go -n 100 --no-pager
```

确认后台登录成功后，从环境文件删除 `DL_ADMIN_PASSWORD` 并重启。不要把明文密码或发布
私钥提交到 Git、复制到公开目录，或保留在 systemd unit 中。

更新查询地址：

```text
GET /api/v1/updates/{product}/{channel}/{target}/{current-version}
```

例如：

```text
https://dl.100ask.net/api/v1/updates/lynx/stable/windows-x86_64/0.9.0
```

响应会返回 `delta` 或 `full` 策略。增量响应始终同时携带完整包 fallback。
正式文件统一位于：

```text
https://dl.100ask.net/Tools/{product}/releases/{channel}/{version}/{file}
```
