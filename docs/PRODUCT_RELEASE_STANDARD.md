# 100ASK 软件发布规范

本规范适用于 LYNX、USBToolBox 及后续桌面软件。目标是让每个产品共用同一条签名、暂存、
审批、发布和更新链，而不是为每个软件复制一套服务器脚本。

## 1. 稳定产品标识

每个产品先确定一个永久小写 slug，只允许 `[a-z][a-z0-9_-]{1,63}`。slug 同时用于清单、
API 与公开目录，发布后不得仅因显示名称变化而修改。

| 软件显示名 | product slug | 正式目录前缀 |
| --- | --- | --- |
| LYNX | `lynx` | `/Tools/lynx/` |
| USBToolBox | `usbtoolbox` | `/Tools/usbtoolbox/` |

## 2. 唯一公开目录

```text
/Tools/<product>/<channel>/<version>/
├── release-set.json
├── <full installer or updater>
└── <optional exact-version delta>
```

- `channel` 只能是 `stable`、`beta` 或 `nightly`。
- `version` 是不带 `v` 的语义版本；目录发布后不可覆盖。
- 安装包、升级完整包、增量包和清单必须在同一个版本目录内。
- `/Tools/<product>/` 可继续放人工资料，但 `stable/`、`beta/`、`nightly/` 子树只能由发布服务写入。
- 根级 `/releases/` 已废弃，只提供 308 跳转，不得保留真实文件。

## 3. 产品仓库职责

产品仓库负责构建资产、生成 SHA-256、使用受保护的 minisign/Tauri 私钥签名，并生成
schema 1 的 `release-set.json`。流水线把清单声明的文件交给通用
`100askTeam/dlfilewebsite/.github/actions/publish-release@release-publisher-v1`；不得自行 SCP
到公开目录，也不得自己拼服务器目标路径。该 Action 先校验产品无关的
`release-channel-probe-v2`，再调用统一上传器。

推荐阶段：

1. tag 仅构建候选和 GitHub Draft；
2. 完成目标机安装、覆盖升级和签名验收；
3. 人工触发 promotion，将发布集上传为 `staged`；
4. 服务端验签、摘要校验和版本单调检查通过后再发布 stable；
5. 反向下载主站资产复核摘要，最后公开 GitHub Release。

不同产品应使用各自的发布仓库与 GitHub Environment 审批。当前服务的
`DL_RELEASE_PUBLIC_KEY` 是单一信任域：多个产品共用该键前必须明确审批并记录；若要求产品
隔离密钥，必须先实现并验证 product→public key 的显式绑定，禁止静默试钥或回退。

## 4. 客户端接口

通用更新接口：

```text
GET /api/v1/updates/<product>/<channel>/<target>/<current-version>
GET /api/v1/tauri/<product>/<channel>/<target>/<current-version>
```

客户端下载 `url` 后必须复核签名与 SHA-256。需要完整包恢复时使用
`X-Update-Mode: full`。产品仓库只配置 API，不配置 `/Tools/...` 中某个版本的静态地址。

示例：

```text
https://dl.100ask.net/api/v1/tauri/lynx/stable/windows-x86_64/0.9.0
https://dl.100ask.net/api/v1/tauri/usbtoolbox/stable/windows-x86_64/1.0.0
```

## 5. 接入清单

- 选定并登记 product slug、发布仓库、目标平台和负责人；
- 确认沿用已审批的共享签名域，或先完成独立 product→public key 绑定；私钥只进入受保护 Secret；
- 构建完整恢复包；有增量时必须精确声明 `from_version`；
- 使用统一发布集格式和上传脚本；
- 添加一个服务端选择测试和一个客户端更新端点契约测试；
- 首次 stable promotion 前完成下载、签名、摘要、安装和升级回退验证。
