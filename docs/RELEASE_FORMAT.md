# 签名发布集格式

一个发布集是只含普通文件的单层目录：

```text
release-set/
├── release-set.json
├── LYNX_0.9.1_x64-setup.exe
└── LYNX_0.9.1_from_0.9.0_x64-delta.exe
```

清单 schema 1 保持向后兼容。需要让下载站只保存差分、完整包留在 GitHub 时使用 schema 2：

```json
{
  "schema_version": 2,
  "product": "lynx",
  "channel": "stable",
  "version": "0.9.1",
  "published_at": "2026-09-11T12:00:00Z",
  "notes": "修复硬件监视与升级流程",
  "assets": [
    {
      "target": "windows-x86_64",
      "kind": "full",
      "storage": "external",
      "file": "LYNX_0.9.1_x64-setup.exe",
      "size": 37000000,
      "sha256": "64位小写十六进制摘要",
      "signature": "Tauri/minisign 签名文件内容的 base64",
      "mirrors": ["https://github.com/100askTeam/example/releases/download/v0.9.1/LYNX_0.9.1_x64-setup.exe"]
    },
    {
      "target": "windows-x86_64",
      "kind": "delta",
      "storage": "site",
      "from_version": "0.9.0",
      "file": "LYNX_0.9.1_from_0.9.0_x64-delta.exe",
      "size": 3500000,
      "sha256": "64位小写十六进制摘要",
      "signature": "补丁文件 minisign 签名的 base64"
    }
  ]
}
```

规则：

- 每个 target 必须声明完整包；增量只是优化，不能成为唯一恢复路径。schema 1 完整包为站内
  文件；schema 2 的完整包可用 `storage: external` 指向 GitHub HTTPS。Windows 增量资产是
  由 LYNX 流水线生成的原生 NSIS 更新器，不是由下载站解释或执行的裸补丁。
- 增量只允许精确的 `from_version -> version`，禁止把相近版本共用一个补丁。
- `1.x` 只允许同 major 增量；发布到 `2.0.0` 必须完整安装。
- `0.x` 额外要求 minor 相同，因此 `0.9.x` 可增量，`0.9.x -> 0.10.0` 必须完整安装。
- 稳定通道不接受 prerelease，镜像必须是无凭据、无 fragment 的 HTTPS URL。
- 文件名不能含目录；服务端拒绝符号链接、摘要/大小不符、重复路由、未知字段以及清单之外的
  任何文件或子目录。schema 2 incoming 只包含 `release-set.json`、`release-set.json.sig` 和
  `storage: site` 的去重资产；`external` 文件不得上传。
- schema 2 的 `release-set.json.sig` 必须覆盖清单原始字节，服务端先用 product 独立公钥验签
  清单，再信任外部资产的 URL、大小、SHA-256 与签名元数据。私钥只在可信发布机或 CI。

服务端校验：

```bash
install -d ./release-keys
install -m 0644 ./release.pub ./release-keys/lynx.pub
DL_RELEASE_PUBLIC_KEYS_DIR=./release-keys dlctl verify ./release-set
```

审批发布后，清单、清单签名和所有 `storage: site` 资产落在：

```text
/Tools/<product>/<channel>/<version>/
```

例如 LYNX v0.9.1 使用 `/Tools/lynx/stable/0.9.1/`；USBToolBox v1.0.1
使用 `/Tools/usbtoolbox/stable/1.0.1/`。站点根级 `/releases` 不存放资产。

更新查询：

- `GET /api/v1/updates/lynx/stable/windows-x86_64/0.9.0` 返回完整的选择结果，包含
  `strategy`、资产摘要和完整包 fallback；
- `GET /api/v1/tauri/lynx/stable/windows-x86_64/0.9.0` 返回 Tauri 更新器格式，默认选择
  精确兼容的增量包；
- 同一 Tauri 请求带通用的 `X-Update-Mode: full` 时强制返回完整包。旧 LYNX 客户端的
  `X-Lynx-Update-Mode: full` 仅作为兼容别名保留；请求头只改变资产选择，不关闭签名校验。
