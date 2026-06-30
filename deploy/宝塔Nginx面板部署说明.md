# 宝塔 / Nginx 面板部署说明

本文档适用于宝塔面板环境，域名按 `dl.100ask.net`，代码目录按 `/home1/dlfile/server_release_bundle`。

## 一、上传文件

把下面两类内容都上传到：

```text
/home1/dlfile/server_release_bundle
```

1. 当前发布版代码
2. 你的资源目录，例如：
   - `Hardware/`
   - `Video/`

## 二、安装与启动

在宝塔终端执行：

```bash
cd /home1/dlfile/server_release_bundle
cp .env.example .env
bash scripts/start.sh
```

如果你希望改成后台常驻，再执行：

```bash
cp deploy/systemd/dl-download-site.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable dl-download-site
systemctl restart dl-download-site
```

## 三、宝塔网站配置

在宝塔里新建站点：

- 域名：`dl.100ask.net`
- 根目录：`/home1/dlfile`
- PHP 版本：`纯静态` 或不启用 PHP

说明：

- 这个站点实际由 Python + Gunicorn 提供服务
- 宝塔里的站点根目录主要用于管理和证书绑定
- Python 程序实际运行目录仍然是 `/home1/dlfile/server_release_bundle`

## 四、宝塔反向代理配置

打开：

- 网站 -> `dl.100ask.net` -> 反向代理

新增反向代理，目标填：

```text
http://127.0.0.1:5000
```

建议代理名称：

```text
dl_download_site
```

## 五、宝塔 Nginx 配置建议

如果你直接改“配置文件”，优先使用这个文件：

- [dl.100ask.net.baota.conf](file:///i:/dlfile/server_release_bundle/deploy/nginx/dl.100ask.net.baota.conf)

它就是按你当前宝塔站点格式改好的最终版。

### 重要说明

- 必须保留宝塔 SSL 区块里这两类注释行原样不动：
  - `#SSL-START SSL相关配置，请勿删除或修改下一行带注释的404规则`
  - `#error_page 404/404.html;`
- 不要保留 `include enable-php-56.conf;`
- 不要保留 `fancyindex` 相关配置
- 不要保留旧的 `rewrite` 引用
- 所有请求统一转发到 `http://127.0.0.1:5000`
- 你发给我的示例里有几处反引号 `` ` ``，Nginx 配置里不能用，必须是正常文本

### 可直接替换版

```nginx
server
{
    listen 80;
    listen 443 ssl http2;
    server_name dl.100ask.net;
    index index.html index.htm default.html;
    root /home1/dlfile;

    if ($server_port !~ 443) {
        rewrite ^(/.*)$ https://$host$1 permanent;
    }

    ssl_certificate     /www/server/panel/vhost/cert/dl.100ask.net/fullchain.pem;
    ssl_certificate_key /www/server/panel/vhost/cert/dl.100ask.net/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;
    add_header Strict-Transport-Security "max-age=31536000" always;
    error_page 497 https://$host$request_uri;

    location ~ ^/(\.user.ini|\.htaccess|\.git|\.svn|\.project|LICENSE|README.md)$ {
        return 404;
    }

    location ~ \.well-known {
        allow all;
    }

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

    access_log /www/wwwlogs/dl.100ask.net.log;
    error_log  /www/wwwlogs/dl.100ask.net.error.log;
}
```

如果 SSL 证书已经由宝塔签发，通常证书目录会是：

```text
/www/server/panel/vhost/cert/dl.100ask.net/
```

## 六、SSL 配置

在宝塔网站面板中：

- 打开“SSL”
- 申请或上传 `dl.100ask.net` 证书
- 开启“强制 HTTPS”

开启后，宝塔通常会自动写入证书路径；如果你手工粘贴 Nginx 配置，请确认路径和宝塔实际证书路径一致。

## 六点五、宝塔里怎么替换

在宝塔网站配置页中：

- 打开 `dl.100ask.net`
- 点击“配置文件”
- 删除旧的 `include enable-php-56.conf;`
- 删除旧的 `fancyindex` 整段
- 删除旧的 `include /www/server/panel/vhost/rewrite/dl.100ask.net.conf;`
- 把 [dl.100ask.net.baota.conf](file:///i:/dlfile/server_release_bundle/deploy/nginx/dl.100ask.net.baota.conf) 内容整体替换进去
- 如果宝塔仍提示 SSL 规则不可修改，优先保留它自动生成的 SSL 注释区块，只替换 `location /`、日志和禁止访问规则部分
- 保存后执行“重载配置”或运行 `nginx -t && nginx -s reload`

## 七、常用操作

### 重建目录页

```bash
cd /home1/dlfile/server_release_bundle
bash scripts/rebuild.sh
```

### 查看服务状态

```bash
systemctl status dl-download-site
```

### 重启服务

```bash
systemctl restart dl-download-site
```

## 八、上线检查

确认以下项目都正常：

- `http://127.0.0.1:5000/admin` 可打开
- `https://dl.100ask.net/` 可访问
- `https://dl.100ask.net/admin` 可登录
- 文件上传后前台能自动显示
