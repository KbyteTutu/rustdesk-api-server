# 部署指南

自建 RustDesk 中心服务器的完整部署指南。三组件对齐官方 Pro 架构（hbbs 信令 / hbbr 中继 / api 账号与地址簿），全部开源、单仓库、docker compose 编排。

基线版本 **0.1**。

## 架构总览

| 组件 | 容器 | 镜像 | 职责 | 端口 |
|---|---|---|---|---|
| hbbs | `hbbs` | `ghcr.io/kbytetutu/rustdesk-hbbs:0.1` | ID 注册、打洞信令、**key exchange**、websocket | 21115, 21116/tcp+udp, 21118 |
| hbbr | `hbbr` | `ghcr.io/kbytetutu/rustdesk-hbbr:0.1` | 流量中继 | 21117, 21119 |
| api | `rustdesk-api` | `ghcr.io/kbytetutu/rustdesk-api:0.1` | 账号登录、地址簿、审计、web 控制台、webclient | 21114 |

三容器共享 `./data` 目录：hbbs 在其中生成/读取服务端密钥对 `id_ed25519(.pub)`，api 只读挂载同一目录以读取公钥。

数据流：

```
客户端 ──登录/地址簿/心跳──▶ api (:21114)
客户端 ──ID注册/打洞/kx────▶ hbbs (:21115/21116/21118)
客户端 ──中继流量──────────▶ hbbr (:21117/21119)
```

## 前置条件

- 一台公网服务器（有公网 IP 或域名）
- 已安装 Docker ≥ 24 与 compose 插件
- 防火墙/安全组放行：`21114, 21115, 21116/tcp, 21116/udp, 21117, 21118, 21119`

## 快速开始

```bash
git clone https://github.com/KbyteTutu/rustdesk-server.git
cd rustdesk-server
cp .env.example .env
# 编辑 .env：RUSTDESK_HOST 改为你的域名或公网 IP
docker compose up -d
```

查看状态（三容器应全部 healthy）：

```bash
docker compose ps
docker compose logs -f hbbs   # 首次启动会打印生成的服务端公钥
```

## 初始化控制台

1. 首次启动时 api 会生成随机初始密码，在服务器上查看：

   ```bash
   docker compose logs rustdesk-api | grep "Admin Password"
   ```

2. 浏览器打开 `http://<RUSTDESK_HOST>:21114/_admin/`，账号 `admin` + 上一步的密码登录，**登录后立即修改密码**
3. （可选）在系统设置中按需开启注册、2FA、OIDC 等

## 客户端配置

RustDesk 客户端 → 设置 → 网络 → 解锁网络配置：

| 项 | 值 |
|---|---|
| ID 服务器 | `<RUSTDESK_HOST>:21116` |
| 中继服务器 | `<RUSTDESK_HOST>:21117` |
| API 服务器 | `http://<RUSTDESK_HOST>:21114` |
| Key | `data/id_ed25519.pub` 文件的内容（一行字符串） |

```bash
cat data/id_ed25519.pub   # 在服务器上查看 Key
```

配置后注册/登录账号即可使用地址簿；浏览器远程控制入口为 `http://<RUSTDESK_HOST>:21114/webclient/`。**已登录状态下直接连接也不会再触发 "Insecure Connection" 弹窗**——hbbs 内置了客户端 1.4.7+ 要求的 key exchange（官方 OSS server 缺失的部分，详见 [背景与原理](./api/docs/rustdesk-1.5-clients.md)）。

## 数据与备份

| 路径 | 内容 | 说明 |
|---|---|---|
| `./data/id_ed25519` | 服务端私钥 | **必须备份**；丢失后所有客户端需重新配置 Key |
| `./data/id_ed25519.pub` | 服务端公钥 | 配置到所有客户端 |
| `./data/api/` | api 数据库（用户/地址簿/审计） | 必须备份 |

备份即打包 `./data` 目录：

```bash
tar czf rustdesk-data-$(date +%F).tgz data/
```

## 升级与回滚

```bash
docker compose pull && docker compose up -d     # 升级到 0.1.x 最新
```

镜像 tag 策略：`0.1`（跟随基线最新）、`0.1.0`（精确版本，用于回滚）、`latest`。回滚：

```bash
# 编辑 docker-compose.yml 中的镜像 tag 为精确版本，如 :0.1.0
docker compose up -d
```

## 可选加固

### 强制登录（MUST_LOGIN）

未登录客户端一律拒绝连接（hbbs 校验 api 签发的 JWT）：

1. `openssl rand -hex 32` 生成密钥，写入 `.env` 的 `RUSTDESK_API_JWT_KEY`
2. 取消 `docker-compose.yml` 中 hbbs 与 api 两处 `RUSTDESK_API_JWT_KEY` 注释，hbbs 增加 `MUST_LOGIN=Y`
3. `docker compose up -d`

### HTTPS / 反向代理

建议生产环境用 Caddy/Nginx 反代 21114（控制台与 API）与 21118/21119（websocket），注意 websocket 需要 `Upgrade` 头透传。反代后 `.env` 中 `RUSTDESK_API_RUSTDESK_API_SERVER` 改为 https 地址。

## 自建镜像

发布版本由 GitHub Actions 构建：推送 `v*.*.*` tag 或手动触发 workflow，自动构建三镜像并推送 `ghcr.io/kbytetutu/rustdesk-*`。

本地构建（以 amd64 为例）：

```bash
# 1. 构建 Rust 二进制（musl）
docker run --rm -v "$PWD":/src -w /src rust:alpine \
  sh -c "apk add --no-cache protoc musl-dev make perl && cargo build --release"

# 2. 摆放到构建上下文
mkdir -p docker/bin/stage/amd64
cp target/release/{hbbs,hbbr,rustdesk-utils} docker/bin/stage/amd64/

# 3. 构建镜像
docker build -f docker/hbbs.Dockerfile -t rustdesk-hbbs:dev .
docker build -f docker/hbbr.Dockerfile -t rustdesk-hbbr:dev .
docker build -f docker/api.Dockerfile -t rustdesk-api:dev .
```

说明：

- api 镜像内自动构建 web 控制台（来自 [lejianwen/rustdesk-api-web](https://github.com/lejianwen/rustdesk-api-web)，构建参数 `ADMIN_WEB_REF` 已固定到具体 commit，更新时修改 `docker/api.Dockerfile` 顶部的 `ARG`）
- Rust 二进制多架构（amd64/arm64）由 CI 构建（rust:alpine 不提供 armv7，已放弃 32 位 ARM）

## 仓库维护

- **api 上游同步**（Go 部分）：`git subtree pull --prefix=api https://github.com/lejianwen/rustdesk-api master --squash`
- **server 上游同步**（Rust 部分）：以 forapi 分支模式合并 `rustdesk/rustdesk-server` 官方 master
- 版本基线 0.1：发版打 tag `v0.1.x` 即可触发 CI

## 许可证

- hbbs/hbbr（Rust 部分）：AGPL-3.0，源自 [rustdesk/rustdesk-server](https://github.com/rustdesk/rustdesk-server) 与 [lejianwen/rustdesk-server](https://github.com/lejianwen/rustdesk-server)
- api（Go 部分，`api/` 目录）：MIT，源自 [lejianwen/rustdesk-api](https://github.com/lejianwen/rustdesk-api)
