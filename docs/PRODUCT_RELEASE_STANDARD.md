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
schema 1 的 `release-set.json`。标准 Tauri 项目可先调用
`100askTeam/dlfilewebsite/.github/actions/prepare-tauri-release@<完整提交 SHA>`，从
`latest.json` 和签名 updater 资产生成并复验发布集。流水线再把清单声明的文件交给通用
`100askTeam/dlfilewebsite/.github/actions/publish-release@<完整提交 SHA>`；不得自行 SCP
到公开目录，也不得自己拼服务器目标路径。该 Action 先校验产品无关的
`release-channel-probe-v2`，再调用统一上传器。

推荐阶段：

1. tag 仅构建候选和 GitHub Draft；
2. 完成目标机安装、覆盖升级和签名验收；
3. 人工触发 promotion，将发布集上传为 `staged`；
4. 服务端验签、摘要校验和版本单调检查通过后再发布 stable；
5. 反向下载主站资产复核摘要，最后公开 GitHub Release。

不同产品应使用各自的发布仓库、GitHub Environment 和 updater 签名密钥。服务器通过
`DL_RELEASE_PUBLIC_KEYS_DIR` 加载 `<product>.pub`，只用清单声明 product 对应的公钥验签；
未知 product、错误密钥、符号链接和非 `.pub` 配置项全部拒绝。兼容的单公钥环境变量只绑定
`lynx`，不会成为多产品通配信任域。

### 已发布版本恢复

下载服务器不得承担从 GitHub Release 下载大资产的工作。公开版本目录整体丢失时，由产品
仓库的人工恢复工作流在 GitHub Runner 上下载并重新验证原发布集，再调用通用
`100askTeam/dlfilewebsite/.github/actions/recover-release@<完整提交 SHA>`。恢复通道使用
`release-channel-probe-v3`，只允许将验签后的 incoming 集合交给
`dlctl restore-published`。

恢复不会创建 release、改变 channel head 或覆盖已有目录。数据库必须已存在相同
product/channel/version 的 published 或 superseded 记录，且规范化 manifest 必须完全一致；
公开目录部分存在、记录缺失或任一字节不一致都会阻断。工作流成功后必须从下载站反向读取
全部唯一资产并复核 SHA-256。该流程同样适用于 `lynx`、`usbtoolbox` 和后续登记产品。

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
- 将产品现有 updater 公钥登记为 `<product>.pub`；私钥只进入受保护 Secret；
- 构建完整恢复包；有增量时必须精确声明 `from_version`；
- 使用统一发布集格式和上传脚本；
- 添加一个服务端选择测试和一个客户端更新端点契约测试；
- 首次 stable promotion 前完成下载、签名、摘要、安装和升级回退验证。
- 为已发布版本配置人工恢复工作流，并将通用恢复 Action 固定到完整提交 SHA。

## 6. 标准自动推送时序

```text
tag -> 多平台构建/签名 -> GitHub Draft -> prepare-tauri-release
    -> 人工 promotion -> 受限 incoming -> 按 product 公钥验签
    -> /Tools/<product>/<channel>/<version>/ 原子发布
    -> 全资产反向 SHA-256 -> 在线更新 API -> GitHub Release 公开
```

tag 阶段不得修改公开下载站；promotion 必须最后才公开 GitHub Release。下载站不得从 GitHub
拉取大文件，丢失版本只能由 Runner 使用 recover Action 恢复既有不可变记录。
