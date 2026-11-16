# 部署指南（审计修复分支）

本分支改变认证、控制通道与配置格式，**不能直接配用旧发布镜像**。
先从同一提交构建 hbbs、hbbr、API 三个镜像，使用固定版本标签。
不要把旧 `0.2` 或 `latest` 镜像的行为当成本分支的验收结果。

## 安全边界

- hbbs 与 API 必须使用相同的 `RUSTDESK_API_JWT_KEY`，至少 32 字节；Compose 强制 `MUST_LOGIN=Y`。
- JWT 固定有效期（默认 `168h`），每次登录有独立会话。不再只延长数据库过期时间。
  API 注销/禁用/改密会撤销数据库会话；hbbs 离线验证 JWT，**已签发令牌在签名到期前仍可能通过 hbbs**。
- API 只挂载公钥副本，不挂载服务端私钥目录。公钥导出服务只在初始化时复制公钥。
- 管理命令只供管理员通过私有 Unix socket 使用，不再开放 TCP 控制端口。
  socket 父目录 `0700`、socket `0600`；空路径禁用控制；Windows 没有网络后备通道。
  API 容器拥有控制能力，必须作为受信任管理组件保护。
- API 与 WebSocket 只发布在主机回环地址。生产必须用 HTTPS/WSS 反向代理。
  WebSocket 不接受额外 KeyExchange：采用 TLS-only，拒绝半加密状态。
  Rust 服务不信任客户端提供的 `X-Real-IP`/`X-Forwarded-For`。
- API 的 Gin 默认不信任代理。需要真实客户端 IP 时，仅配置实际代理 CIDR，禁止 `0.0.0.0/0`。
- API 日志只写 stdout，Compose 使用 local 驱动、每服务 3×10 MB 轮转。不得记录密码、JWT、对称密钥。

## 首次启动

需要 Linux、Docker Engine 与 Compose 插件，以及 HTTPS/WSS 反向代理。

```sh
git submodule update --init --recursive
cp .env.example .env
chmod 600 .env
openssl rand -hex 32
```

编辑 `.env`：

| 配置 | 要求 |
|---|---|
| `RUSTDESK_IMAGE_TAG` | 从当前修复提交构建的固定镜像标签 |
| `RUSTDESK_HOST` | 客户端能解析到的公网域名/IP，不是容器名 |
| `RUSTDESK_API_URL` | 外部 HTTPS 地址，如 `https://desk.example.com` |
| `RUSTDESK_API_JWT_KEY` | 上述随机密钥，hbbs/API 共用 |
| `RUSTDESK_API_ADMIN_PASSWORD` | 首次初始化 admin 的密码，8–72 字节；不会打印到日志 |

`.env` 不得提交、分享或放入公开备份。已有用户库不会被初始化密码覆盖。
先备份数据库及私钥，再启动：

```sh
docker compose config --quiet
docker compose up -d
docker compose ps
```

启动顺序为 hbbs 健康 → hbbr / 公钥导出 → API。
API 登录页为 `https://desk.example.com/_admin/`，初始用户 `admin`。
API 健康检查访问 `/api/login-options`，不仅检查进程存在。

### 网络

| 用途 | 端口 | 暴露方式 |
|---|---|---|
| NAT / ID | 21115/TCP、21116/TCP+UDP | 公网 |
| 中继 | 21117/TCP | 公网 |
| API / 控制台 | 21114/TCP | 127.0.0.1，经 HTTPS 代理 |
| hbbs WebSocket | 21118/TCP | 127.0.0.1，经 WSS 代理 |
| hbbr WebSocket | 21119/TCP | 127.0.0.1，经 WSS 代理 |

代理必须正确透传 WebSocket Upgrade。部署后用实际客户端验证 WSS 路径及原生 TCP 连接。
未做实际客户端矩阵测试前，不应承诺所有版本均兼容。

## 客户端

- ID 服务器：`<RUSTDESK_HOST>:21116`
- 中继服务器：`<RUSTDESK_HOST>:21117`
- API：`.env` 中的 HTTPS 地址
- Key：服务器上的 `data/id_ed25519.pub` 内容（公钥，不是 `data/id_ed25519`）

设备遥测接口必须携带有效 Bearer 会话，且 id/uuid 与登录会话及设备所有者匹配。
不发送认证头的旧客户端不会被匿名放行，需要升级/调整客户端并验收。

## 身份与数据约束

- 密码仅支持 bcrypt；旧 MD5 不做自动升级。需人工重置旧密码，长度 8–72 **字节**。
- `app.token-expire` 必须是 `168h` 这样的时长字符串；废弃 `jwt.expire-duration`。
- LDAP 只支持校验证书的 LDAPS，连接/请求超时 5 秒。自签 CA 用 `tls-ca-file`，不可关闭校验。
  AD 默认检查 `userAccountControl` 的 ACCOUNTDISABLE；非 AD 必须显式配置状态字段和值。
- 启用 LDAP 后密码认证只走 LDAP，失败不回退本地密码。新 LDAP 账号绑定目录 URL+DN，
  不按用户名/邮箱接管本地账号。旧 LDAP 用户没有绑定来源时也拒绝自动认领；
  上线前由管理员核验并明确处理身份冲突，代码不提供自动迁移/合并。
- OAuth 不按邮箱自动合并账号。绑定必须由已登录用户确认，登录结果只能消费一次。
  provider 标识、类型、issuer 创建后不可修改；已有绑定的 provider 不可直接删除。
- 启动前检查 OAuth provider、provider/subject、user/provider 和设备 ID 重复数据。
  冲突会中止启动，必须人工核实处理；不会偷偷去重或自动重绑。
- MySQL/PostgreSQL 数据库须预先创建。SQLite 开启 WAL、busy timeout，串行写入。
- 最后一个启用管理员不能删除、降权或禁用；删除用户时清理关系，保留匿名化登录审计。
- 上传/OSS/全局 Redis/缓存框架已移除，没有旧路由或兼容层；欢迎语是纯文本，不再解析 HTML。

## 数据与备份

| 路径/卷 | 内容 |
|---|---|
| `./data/id_ed25519` | 私钥，0600，严格保密，必须备份 |
| `./data/id_ed25519.pub` | 公钥 |
| `./data/api/` | API 数据库（SQLite 包括 WAL） |
| `public-key` 命名卷 | 可重新导出的公钥副本 |
| `hbbs-control` / `hbbr-control` | 临时控制 socket，不作为业务数据备份 |

先停写或使用数据库的一致性备份，不要在写入中仅复制 SQLite 主文件而遗漏 WAL。
私钥损坏会使启动失败，不会自动替换。轮换密钥需人工安排维护窗口并更新全部客户端。
更新固定镜像标签前备份数据；数据库变更后不能把直接降级镜像当作可靠回滚。

## 从源码验证与构建

```sh
(cd api && go test -mod=readonly -race ./... && go vet -mod=readonly ./... && go build -mod=readonly ./...)
(cd web && npm ci && npm run build && npm audit --audit-level=high)
# 原生 Rust 工具链须安装 protoc；依赖以 Cargo.lock 为准
cargo test --locked
cargo build --locked --release
```

部署到 Alpine 的 Rust 二进制须使用 musl 工具链。以 amd64 为例：

```sh
docker run --rm -v "$PWD":/src -w /src rust:alpine \
  sh -c 'apk add --no-cache protoc musl-dev make perl && cargo test --locked && cargo build --locked --release'
mkdir -p docker/bin/amd64
cp target/release/hbbs target/release/hbbr target/release/rustdesk-utils docker/bin/amd64/
TAG=audit-fixed
docker build --build-arg TARGETARCH=amd64 -f docker/hbbs.Dockerfile -t ghcr.io/kbytetutu/rustdesk-hbbs:$TAG .
docker build --build-arg TARGETARCH=amd64 -f docker/hbbr.Dockerfile -t ghcr.io/kbytetutu/rustdesk-hbbr:$TAG .
docker build --build-arg TARGETARCH=amd64 -f docker/api.Dockerfile -t ghcr.io/kbytetutu/rustdesk-api:$TAG .
```

本地最小端到端验证（不替代真实客户端、LDAP/OIDC 和发布镜像联调）：

```sh
# 先生成 target/debug/hbbs、hbbr；测试容器镜像须已在本机
cargo build --locked
docker pull alpine:3.21
python3 scripts/audit-smoke.py
```

`scripts/audit-smoke.py` 使用临时数据、随机测试密码、无网络容器和仅主机回环 API，
验证空库启动、管理员建用户、长密码登录、私有 socket 控制、普通用户拒绝访问、
遥测身份边界和退出撤销；结束后删除容器与测试数据。可用 `RUSTDESK_SMOKE_IMAGE`
指定本机已有且可运行 Rust 二进制的镜像。它不读取部署 `.env` 或修改现有服务。

CI 先验证 Go/Rust/前端，再发布三镜像；Rust 按 amd64/arm64 原生构建，API 使用 CGO/musl。
第三方 Actions 固定提交，构建带 provenance/SBOM。基础镜像仍使用标签，供应链完全可复现性
还需维护经过验证的镜像 digest 与对应工具链版本，不应把 `--locked` 等同于所有输入均不可变。
