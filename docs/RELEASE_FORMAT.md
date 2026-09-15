# 签名发布集格式

一个发布集是只含普通文件的单层目录：

```text
release-set/
├── release-set.json
├── LYNX_0.9.1_x64-setup.exe
└── LYNX_0.9.1_from_0.9.0_x64-delta.exe
```

清单 schema 1：

```json
{
  "schema_version": 1,
  "product": "lynx",
  "channel": "stable",
  "version": "0.9.1",
  "published_at": "2026-09-11T12:00:00Z",
  "notes": "修复硬件监视与升级流程",
  "assets": [
    {
      "target": "windows-x86_64",
      "kind": "full",
      "file": "LYNX_0.9.1_x64-setup.exe",
      "size": 37000000,
      "sha256": "64位小写十六进制摘要",
      "signature": "Tauri/minisign 签名文件内容的 base64",
      "mirrors": ["https://github.com/100askTeam/example/releases/download/v0.9.1/LYNX_0.9.1_x64-setup.exe"]
    },
    {
      "target": "windows-x86_64",
      "kind": "delta",
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

- 每个 target 必须有完整包；增量只是优化，不能成为唯一恢复路径。Windows 增量资产是
  由 LYNX 流水线生成的原生 NSIS 更新器，不是由下载站解释或执行的裸补丁。
- 增量只允许精确的 `from_version -> version`，禁止把相近版本共用一个补丁。
- `1.x` 只允许同 major 增量；发布到 `2.0.0` 必须完整安装。
- `0.x` 额外要求 minor 相同，因此 `0.9.x` 可增量，`0.9.x -> 0.10.0` 必须完整安装。
- 稳定通道不接受 prerelease，镜像必须是无凭据、无 fragment 的 HTTPS URL。
- 文件名不能含目录；服务端拒绝符号链接、摘要/大小不符、重复路由、未知字段以及清单之外的
  任何文件或子目录。CI/SCP 只传 `release-set.json` 与清单声明的去重资产。
- 服务器只持有 minisign 公钥。私钥在可信发布机或 CI 的受保护 Secret 中签名。

服务端校验：

```bash
DL_RELEASE_PUBLIC_KEY_FILE=./release.pub dlctl verify ./release-set
```

审批发布后，同一发布集的清单、完整安装包、增量包及其他声明资产全部落在：

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
