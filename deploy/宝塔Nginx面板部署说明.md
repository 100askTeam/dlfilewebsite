# 宝塔 Nginx 部署（当前生产布局）

当前固定参数：

- 域名：`dl.100ask.net`
- 公开文件：`/home1/dlfile`
- Go 代码：`/home1/dladmin-code/current`
- 私有状态：`/home1/dlfile-state`
- Go 服务：`127.0.0.1:5001`

宝塔中不再配置 PHP、fancyindex 或旧 Flask/Gunicorn。直接使用
[`nginx/dl.100ask.net.baota.conf`](nginx/dl.100ask.net.baota.conf)，在面板中确认证书路径后执行：

```bash
/www/server/nginx/sbin/nginx -t
/www/server/nginx/sbin/nginx -s reload
curl -fsS http://127.0.0.1:5001/ >/dev/null
curl -fsS https://dl.100ask.net/ >/dev/null
```

修改 Go 源码不会自动覆盖 `/home1/dlfile`，CI 也不会自动重启生产服务。
