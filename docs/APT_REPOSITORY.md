# 通用签名 APT 接收端

下载站不编译 deb、不生成 `Packages`/`Release`，也不保存 APT 私钥。完整仓库由
`dshanpi-build` 生成和签名，上传到 `/home1/dlfile/_apt-upload/<job-id>`；服务器只使用
已安装的公钥验签、检查不可变 pool 和原子切换 suite 元数据。

公开地址统一为：

```text
https://dl.100ask.net/apt
```

`common` component 存放公共包，各产品使用自己的 component。`noble` 是 stable，
`noble-testing` 是 testing。下载站不包含产品包清单或设备树知识。

## 服务器安装

```bash
sudo install -d -o root -g root -m 0755 /etc/dladmin/apt-keys
sudo install -o root -g root -m 0644 dshanpi.gpg /etc/dladmin/apt-keys/dshanpi.gpg
sudo install -o root -g root -m 0755 deploy/apt/dl-apt-import /usr/local/libexec/dl-apt-import
sudo install -d -o apt-publisher -g apt-publisher -m 0750 /home1/dlfile/_apt-upload
sudo install -d -o root -g root -m 0755 /home1/dlfile/apt /home1/dlfile/_apt-metadata
```

为专用低权限 SSH 用户 `apt-publisher` 配置密码，并仅允许它通过 sudo 调用固定入口：

```text
apt-publisher ALL=(root) NOPASSWD: /usr/local/libexec/dl-apt-import *
```

入口脚本严格限制 job id，并由真正的导入器再次验签。该用户不能直接写公开 `/apt`。
部署新版服务后执行：

```bash
nginx -t
systemctl reload nginx
systemctl restart dladmin-go
```

## 导入门禁

- `SHA256SUMS.asc` 必须由 `/etc/dladmin/apt-keys/dshanpi.gpg` 验证通过。
- 每个 suite 的 `InRelease`、`Release.gpg` 和 Release SHA256 均重新验证。
- Packages、Packages.gz、Packages.xz 必须一致，且引用的 pool 文件大小和 SHA256 正确。
- 相同 package/version/architecture 不允许对应不同内容。
- pool 文件只增不改；旧版本默认永久保留。
- publication 必须基于当前完整仓库状态哈希，拒绝并发造成的旧状态覆盖。
- 普通下载站文件管理接口不能创建、上传或删除 `apt`、`_apt-upload` 与
  `_apt-metadata`，后两者也不对公网直接提供。

APT 私钥和产品 release lock 只存在于 `dshanpi-build`。stable 晋级也在该仓库完成，
下载站不知道某个包属于 CM5、R1 或 Avaota。
