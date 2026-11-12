# rustdesk-server (self-hosted)

RustDesk 自建中心服务器：**hbbs（信令）+ hbbr（中继）+ api（账号/地址簿/控制台）** 三组件单仓库、docker compose 编排，架构对齐官方 Pro 版，全部开源。

基线版本 **0.1**。完整部署文档见 **[DEPLOY.md](./DEPLOY.md)**。

## 为什么有这个仓库

- 官方 OSS `rustdesk/rustdesk-server` 没有账号/地址簿体系（那是 Pro 闭源组件），且从客户端 1.4.7 起，**登录账号后的连接会被强制要求 key exchange**，OSS hbbs 不实现该握手，导致 "Failed to secure tcp: deadline has elapsed"（[rustdesk-api#524](https://github.com/lejianwen/rustdesk-api/issues/524)）
- 本仓库的 Rust 部分（`src/`）基于 [lejianwen/rustdesk-server](https://github.com/lejianwen/rustdesk-server) 的 forapi 分支：**内置服务端 key exchange**，登录态连接正常
- Go 部分（`api/`，subtree 引入 [lejianwen/rustdesk-api](https://github.com/lejianwen/rustdesk-api)）提供账号、地址簿、审计、web 控制台、web client

## 架构

| 组件 | 镜像 | 职责 | 端口 |
|---|---|---|---|
| hbbs | `ghcr.io/kbytetutu/rustdesk-hbbs:0.1` | ID 注册、打洞信令、key exchange | 21115, 21116/tcp+udp, 21118 |
| hbbr | `ghcr.io/kbytetutu/rustdesk-hbbr:0.1` | 流量中继 | 21117, 21119 |
| api | `ghcr.io/kbytetutu/rustdesk-api:0.1` | 账号、地址簿、审计、控制台 `/_admin/`、web client `/webclient/` | 21114 |

## 快速开始

```bash
git clone https://github.com/KbyteTutu/rustdesk-server.git
cd rustdesk-server
cp .env.example .env   # 编辑 RUSTDESK_HOST 为你的域名或公网 IP
docker compose up -d
docker compose logs rustdesk-api | grep "Admin Password"   # 初始管理员密码
```

客户端配置与后续步骤见 [DEPLOY.md](./DEPLOY.md)。

## 仓库结构

```
├── src/               # Rust：hbbs / hbbr / rustdesk-utils（fork 自 lejianwen forapi）
├── libs/hbb_common/   # Rust 公共库（submodule）
├── api/               # Go：rustdesk-api（git subtree 引入，含自身 docs/）
├── docker/            # 三个组件各自的 Dockerfile（api.Dockerfile / hbbs.Dockerfile / hbbr.Dockerfile）
├── docker-compose.yml # 三组件编排
├── .github/workflows/ # ghcr.yml：tag v*.*.* 触发，构建多架构镜像推送 GHCR
└── DEPLOY.md          # 部署指南
```

## 维护

- 更新 Go 部分：`git subtree pull --prefix=api https://github.com/lejianwen/rustdesk-api master --squash`
- 更新 Rust 部分：合并 `rustdesk/rustdesk-server` 官方 master 到 forapi
- 发版：打 tag `v0.1.x` 触发 CI 构建镜像

## 许可证

- `src/`（Rust）：AGPL-3.0
- `api/`（Go）：MIT
