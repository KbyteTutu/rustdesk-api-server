# RustDesk 1.4.7+ 客户端登录后无法连接：根因与本仓库部署方案

## 现象

RustDesk 客户端 **1.4.7 及以上（含 1.5.x）** 登录 API 账号后，所有连接一直停留在「连接中」，
最终失败并提示：

```text
Failed to secure tcp: deadline has elapsed
```

而**退出账号登录**后可以正常连接（参考 [#524](https://github.com/lejianwen/rustdesk-api/issues/524)、
[#514](https://github.com/lejianwen/rustdesk-api/issues/514)）。

## 根因

客户端登录后持有 `access_token`（bearer 凭证），此时 `client.rs` 中
`legacy_secure = !key.is_empty() && !token.is_empty()` 为真：TCP 连上 hbbs（21116 端口）后，
客户端**先等服务器发起 rendezvous key exchange**，握手成功才把 token 加密发出。
**官方开源版 rustdesk-server（hbbs，含最新 1.1.16）从不发起该交换**——这是
rustdesk-server-pro 独有功能。客户端等待 18 秒超时后**主动放弃连接以保护 token 不明文发送**。

官方客户端曾有 3 个修复 PR（rustdesk#15743 / #15809 / #16700，容忍不发握手的服务器），
全部被官方关闭未合并。**API 服务器不在打洞/中继协议路径上，无法在 API 侧修复**，必须让
hbbs 学会发起 key exchange。

## 影响矩阵

| 服务器 | 未登录客户端 | 登录客户端（1.4.7+） |
| --- | --- | --- |
| 官方 `rustdesk/rustdesk-server`（≤1.1.16） | ✅ | ❌ 超时失败 |
| 本仓库 `full-s6` 镜像（默认官方 server 底座） | ✅ | ❌ 超时失败 |
| lejianwen 血统 server（`rustdesk-server-s6` ≥ v0.1.0，含 [KbyteTutu/rustdesk-server](https://github.com/KbyteTutu/rustdesk-server)） | ✅ | ✅ |

lejianwen 血统的 server 自 2025-08-31（commit `872f1f9`）起已实现完整服务端 key exchange
（TCP/WS 均覆盖），已发布的 `lejianwen/rustdesk-server-s6:v0.1.2` 即包含。

## 方案：自维护一体链（GHCR）

两个 fork 组成一条自维护链路，单容器部署：

```text
KbyteTutu/rustdesk-api（本仓库，Go API）
  └─ Actions「Build」→ ghcr.io/kbytetutu/rustdesk-api
KbyteTutu/rustdesk-server（Rust hbbs/hbbr，已内置 key exchange）
  └─ Dockerfile FROM ↑ + s6-overlay + hbbs/hbbr
  └─ Actions「Build and publish to ghcr.io」→ ghcr.io/kbytetutu/rustdesk-server-s6
服务器：单容器跑 rustdesk-server-s6（API + hbbs + hbbr）
```

### 构建镜像

1. **API 镜像**：GitHub → `KbyteTutu/rustdesk-api` → Actions → **Build** → Run workflow
   （或推送 `v*.*.*` tag 自动触发）。产物：`ghcr.io/kbytetutu/rustdesk-api:latest`。
   Docker Hub 推送可跳过（`SKIP_DOCKER_HUB=true`）。
2. **一体镜像**：GitHub → `KbyteTutu/rustdesk-server` → Actions → **Build and publish to
   ghcr.io** → Run workflow（forapi 分支，无需 tag，默认推 `latest`；推 `v*.*.*` tag 则
   同时出版本号镜像）。产物：`ghcr.io/kbytetutu/rustdesk-server-s6:latest`。

首次发布后建议在 GitHub → Packages 检查两个镜像的可见性（默认私有，服务器 `docker login
ghcr.io` 拉取即可；要公开改 package settings）。

### 服务器部署（docker-compose 全量）

```yaml
services:
  rustdesk:
    image: ghcr.io/kbytetutu/rustdesk-server-s6:latest
    container_name: rustdesk
    ports:
      - 21114:21114   # API http / 管理后台 /_admin/
      - 21115:21115   # hbbs NAT 测试
      - 21116:21116   # hbbs ID 注册 / 打洞（TCP）
      - 21116:21116/udp # hbbs 打洞（UDP）
      - 21117:21117   # hbbr 中继
      - 21118:21118   # hbbs websocket（web 客户端）
      - 21119:21119   # hbbr websocket
    environment:
      - TZ=Asia/Shanghai
      - RELAY=<服务器IP或域名>:21117
      - ENCRYPTED_ONLY=1
      - MUST_LOGIN=N        # 是否强制登录后才能发起连接
      - RUSTDESK_API_RUSTDESK_ID_SERVER=<服务器IP或域名>:21116
      - RUSTDESK_API_RUSTDESK_RELAY_SERVER=<服务器IP或域名>:21117
      - RUSTDESK_API_RUSTDESK_API_SERVER=http://<服务器IP或域名>:21114
    volumes:
      - ./data/server:/data          # hbbs/hbbr 密钥对（id_ed25519）
      - ./data/api:/app/data         # API 数据库
      - ./data/server:/app/conf/data # 密钥共享给 API，免配 RUSTDESK_API_RUSTDESK_KEY
    restart: unless-stopped
```

管理后台：`http://<服务器IP或域名>:21114/_admin/`（默认 admin/admin，及时改密）。

### 从「官方分体部署」迁移

原分体（rustdesk-api 容器 + 官方 hbbs/hbbr 容器）迁移到一体链，关键是**保持密钥与数据不变**：

1. 停旧容器，备份：
   - API 数据目录（sqlite 数据库所在 volume）
   - hbbs 的密钥对 `id_ed25519` / `id_ed25519.pub`（官方镜像通常在工作目录 volume 里）
2. 按上面 compose 建目录：
   - `./data/api` ← 放入 API 数据库文件
   - `./data/server` ← 放入 `id_ed25519`、`id_ed25519.pub`（**客户端配置的 key 就是
     `id_ed25519.pub` 内容，保持不变则所有客户端无需改动**）
3. `docker compose up -d`，按下面「验证」确认。

### 配置一致性清单

- **key 三处一致**：hbbs 密钥对（`/data/id_ed25519.pub`）、API（通过 `/app/conf/data` 挂载
  自动读取，或显式配 `RUSTDESK_API_RUSTDESK_KEY`）、客户端网络设置里填写的 Key。
- **客户端服务器配置**：ID 服务器 = `<host>:21116`，中继 = `<host>:21117`，API =
  `http://<host>:21114`。
- `ENCRYPTED_ONLY=1` 建议开启（拒绝未加密客户端）；走反代/HTTPS 时 API 地址照常填外网地址。

### 验证

1. 容器健康：`docker ps` 健康、`docker logs` 无报错。
2. **登录态连接**（核心验证）：客户端登录账号 → 发起连接 → 应正常建立。需要看握手细节时
   在 compose 加 `- RUSTDESK_LOG=debug`（hbbs 日志会出现 `KeyExchange symetric key set`）。
3. 未登录客户端连接正常（回归确认）。

### 回滚

一体链出问题想退回原分体：恢复旧 compose 与容器即可（`./data/server` 里的密钥对就是原
hbbs 的，客户端配置无需变化）。

## 备选路径

- **full-s6（本仓库自带）**：`Dockerfile_full_s6` 已支持 `RUSTDESK_SERVER_IMAGE` 构建参数
  （Actions Build 的同名输入），把底座换成 `ghcr.io/kbytetutu/rustdesk-server-s6:latest`
  即可获得 key exchange 能力；或维持官方底座 + 手动合并社区补丁
  （[rustdesk-server#745](https://github.com/rustdesk/rustdesk-server/pull/745)）。
- **临时规避**（不换服务器时）：客户端退出账号登录；或全部走 wss（`secure_tcp` 对 ws
  传输直接跳过）。
