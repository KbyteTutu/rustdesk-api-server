# rustdesk-api-server

RustDesk 自建中心服务器：**hbbs（信令）+ hbbr（中继）+ api（账号/地址簿/控制台）** 三组件单仓库、docker compose 编排，架构对齐官方 Pro 版，全部开源。

**已适配 RustDesk 1.5.3 客户端（登录态连接正常），镜像支持 amd64 / arm64。**

## 为什么 fork

- 上游 [lejianwen/rustdesk-api](https://github.com/lejianwen/rustdesk-api) **已超过一年无人维护**：issue/PR 长期没有任何响应，多个已知 bug 的修复 PR 挂在队列里没人合并（我们已选择性合并其中的一部分，详见 [docs/merged-upstream-prs.md](./docs/merged-upstream-prs.md)）
- 官方 OSS `rustdesk/rustdesk-server` 没有账号/地址簿体系（那是 Pro 闭源组件），且从客户端 1.4.7 起，**登录账号后的连接被强制要求 key exchange**，官方 OSS hbbs 不实现该握手，导致 "Failed to secure tcp: deadline has elapsed"（[rustdesk-api#524](https://github.com/lejianwen/rustdesk-api/issues/524)）
- 本仓库把三个组件收进一个仓库统一维护，重新适配了新版本客户端：Rust 部分（`src/`）基于 [lejianwen/rustdesk-server](https://github.com/lejianwen/rustdesk-server) forapi 分支（**内置服务端 key exchange**），Go 部分（`api/`，subtree 引入 rustdesk-api）提供账号、地址簿、审计、web 控制台、web client

## 开发方式

本仓库的改造与维护基于 **GLM 5.3 模型编码 + 人工审查修改** 完成：适配方案调研、协议分析、CI/镜像管线、控制台 UI 定制由 AI 辅助生成，关键决策与结果经过手动验证（本地 compose 冒烟 + GHCR 发布镜像端到端复验）。

**欢迎添砖加瓦**：issue、PR（修复、新功能、翻译、文档）都欢迎，本仓库保证及时响应。

## 架构

| 组件 | 镜像（GHCR，多架构 amd64/arm64） | 职责 | 端口 |
|---|---|---|---|
| hbbs | `ghcr.io/kbytetutu/rustdesk-hbbs` | ID 注册、打洞信令、key exchange | 21115, 21116/tcp+udp, 21118 |
| hbbr | `ghcr.io/kbytetutu/rustdesk-hbbr` | 流量中继 | 21117, 21119 |
| api | `ghcr.io/kbytetutu/rustdesk-api` | 账号、地址簿、审计、控制台 `/_admin/`、web client `/webclient/` | 21114 |

## 快速开始

```bash
git clone https://github.com/KbyteTutu/rustdesk-api-server.git
cd rustdesk-api-server
cp .env.example .env   # 编辑 RUSTDESK_HOST 为你的域名或公网 IP
docker compose up -d
docker compose logs rustdesk-api | grep "Admin Password"   # 初始管理员密码
```

客户端配置与后续步骤见 **[DEPLOY.md](./DEPLOY.md)**（完整部署指南）。

## 仓库结构

```
├── src/               # Rust：hbbs / hbbr / rustdesk-utils（fork 自 lejianwen forapi）
├── libs/hbb_common/   # Rust 公共库（submodule）
├── api/               # Go：rustdesk-api（git subtree 引入，含自身 docs/）
├── web/               # Vue 控制台（git subtree 引自 lejianwen/rustdesk-api-web，已定制）
├── docker/            # 三个组件各自的 Dockerfile
├── docs/              # 已合并的上游未合并 PR 详情等文档
├── docker-compose.yml # 三组件编排
├── .github/workflows/ # ghcr.yml：tag v*.*.* 触发，构建 amd64+arm64 镜像推送 GHCR
└── DEPLOY.md          # 部署指南
```

## 与上游的关系

- 上游 `lejianwen/rustdesk-api` 超过一年不维护，本仓库继续跟进新版本客户端（已适配 1.5.3）
- 选择性合并了上游未被合并的 bug 修复 PR，取舍理由与验证记录见 [docs/merged-upstream-prs.md](./docs/merged-upstream-prs.md)
- 控制台 UI 与品牌由本仓库自行维护（`web/` subtree）

## 维护

- 更新 Go 部分：`git subtree pull --prefix=api https://github.com/lejianwen/rustdesk-api master --squash`
- 更新控制台：`git subtree pull --prefix=web https://github.com/lejianwen/rustdesk-api-web master --squash`（注意保留本地定制）
- 更新 Rust 部分：合并 `rustdesk/rustdesk-server` 官方 master 到 forapi
- 发版：打 tag `v*.*.*` 触发 CI 构建镜像

## 许可证

- `src/`（Rust）：AGPL-3.0
- `api/`（Go）：MIT
