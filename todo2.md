# 代码审查报告（第二轮）(TODO2)

本文档为对 `rustdesk-api-server` 分支 `forapi`（提交 `a6f4dfd`）的第二轮独立代码审查结果，
审查范围：`src/`（hbbs/hbbr Rust 核心）、`api/`（Go 服务端）、`web/`（Vue 控制台）、`ui/`、`docker/`、`.github/workflows/`。
与前一轮审查清单 [`todo.md`](./todo.md) 有部分重叠，重叠项已在各条目标注 `≈ todo.md #N`。

审查原则：仅审查，不执行实际修改。

---

## P0：高危安全与远程 DoS（优先修复）

- [ ] **S1. JWT 密钥打印到日志**
  - **位置**：`src/jwt.rs:16` — `println!("secret: {:}", SECRET.to_string());`
  - **风险**：每次 `generate_token` 都把签名密钥打到 stdout。任何能看容器日志/进程输出的人即可伪造任意用户 token；配合 MUST_LOGIN 模式等于全面失守。
  - **改法**：直接删除该行。`≈ todo.md #19`

- [ ] **S2. MUST_LOGIN 空密钥 fail-open（登录强制可被绕过）**
  - **位置**：`src/jwt.rs:8`（`SECRET` 默认空串）+ `src/rendezvous_server.rs:~870`（`handle_punch_hole_request`）
  - **风险**：逻辑为 `if MUST_LOGIN { if token.is_empty() { fail } else if !jwt::SECRET.is_empty() { verify } }`。
    key 未配置时（最易犯的部署错误）MUST_LOGIN=Y 只检查 token 非空，**任意字符串通过认证**。
  - **改法**：fail-closed——MUST_LOGIN=Y 且 SECRET 为空时启动即报错退出；验证失败一律拒绝。`≈ todo.md #19`

- [ ] **S3. 远程 DoS：KeyExchange `unwrap()` panic（单包打挂 hbbs）**
  - **位置**：`src/rendezvous_server.rs:623-624`
    ```rust
    let their_pk: [u8; 32] = ex.keys[0].to_vec().try_into().unwrap();
    let cryptobox: [u8; 48] = ex.keys[1].to_vec().try_into().unwrap();
    ```
  - **风险**：恶意客户端发送 2 个长度错误的 key → panic。`Cargo.toml` release 配置 `panic = "abort"` → 整个 hbbs 进程退出。
  - **改法**：`.try_into().ok()` + 不合法时记日志断开该连接，严禁 panic。`≈ todo.md #1`

- [ ] **S4. 远程 DoS：密钥解密失败 `panic!`**
  - **位置**：`src/rendezvous_server.rs:1619` — `Err(e) => panic!("Error while opening the seal key{:?}", e)`（`get_symetric_key_from_msg` 内 `box_::open` 失败分支）
  - **风险**：损坏/恶意数据同样触发 panic=abort 全进程退出。
  - **改法**：返回 `Result`/`Option`，由调用方断开连接。`≈ todo.md #1`

- [ ] **S5. gin 信任所有代理 + LoginLimiter 封禁可绕过**
  - **位置**：`api/http/http.go:19-28`（`trust-proxy` 默认空 → gin trusted-all-proxies）+ `api/http/middleware/limiter.go`（对所有路由用 `c.ClientIP()` 做封禁判定）
  - **风险**：反代部署下伪造 `X-Forwarded-For` 可绕过 IP 封禁、污染登录日志/审计，甚至把任意 IP 打进黑名单。
  - **改法**：默认只信任 loopback（`127.0.0.1,::1`），文档强调反代场景必须显式配置 `trust-proxy`。与 `todo.md #3`（LoginLimiter 逻辑失效）互补，需一并修

- [ ] **S6. api JWT key 为空时降级为可预测 token**
  - **位置**：`api/service/user.go:88-91` — `if key == "" { token = utils.Md5(u.Username + time.Now().String()) }`
  - **风险**：`api/conf/config.yaml` 默认 `jwt.key: ""`，默认路径即弱 token（用户名+时间戳的 MD5 可爆破）。同时使 hbbs 侧无法用 JWT 验证（联动 S2）。
  - **改法**：启动时 key 为空则生成随机密钥并写回配置/落盘，移除 Md5 fallback。与 `todo.md #17`（MD5 密码 fallback）同属"删除弱密码学 fallback"，可一并处理

---

## P1：中危 / Bug

- [ ] **M1. `CREATE DATABASE IF NOT EXISTS` 字符串拼接**
  - **位置**：`api/cmd/apimain.go:229`
  - **风险**：MySQL 路径下 dbName 直接拼进 DDL，配置值可注入。低危（需要控制配置文件），但应参数化/校验。

- [ ] **M2. login_log 关联错列**
  - **位置**：`api/service/user.go:114` — `llog.UserTokenId = ut.UserId` 应为 `ut.Id`（upstream bug）。`≈ todo.md #13`

- [ ] **M3. 未检查的类型断言 panic**
  - **位置**：`api/http/controller/api/webClient.go:47` — `t := (*j)["share_token"].(string)`
  - **风险**：非字符串 JSON 直接 panic（gin Recovery 兜 500，产生日志噪音）。应 `v, ok := ...; if !ok { fail }`。`≈ todo.md #14`

- [ ] **M4. `.env` 与 `db_v2.sqlite3` 被 git 追踪**
  - **风险**：`.env` 提交是泄密隐患；sqlite 是开发残留（`.gitignore` 已列 `db_v2.sqlite3` 但已追踪不生效）。
  - **改法**：`git rm --cached .env db_v2.sqlite3`，`.gitignore` 补 `.env`。`≈ todo.md #8`

- [ ] **M5. hbbr `PORT` 语义陷阱**
  - **位置**：`src/hbbr.rs:28-31` — `PORT` env 被解释为 *hbbs* 端口，hbbr 自己监听 PORT+1
  - **风险**：用户给 hbbr 设 `PORT=21117` 会实际监听 21118（upstream 行为）。至少加启动日志警告或文档标注。

- [ ] **M6. `src/mod.rs` 死文件**
  - **内容**：声明 `mod sled_async;` 但文件不存在，未被编译，纯残留 → 删除。

---

## P2：优化 / 改进建议

- [ ] **O1. CI 没有任何测试步骤**
  - **位置**：`.github/workflows/ghcr.yml` 只 build。
  - **改法**：加 `go test ./...`（backport 时手动跑过全绿）；rust 侧加 `cargo check`。cargo 构建每 tag 全量无缓存（~10min+），可加 registry/bin 缓存。`≈ todo.md #26`

- [ ] **O2. `docker/api.Dockerfile`**
  - 构建中 `go mod tidy` 会改 go.mod 且依赖网络，应改 `go mod download`；
  - runtime `alpine:3` 缺 `ca-certificates`（LDAP/OAuth TLS 必挂）和 `tzdata`（`TZ=Asia/Shanghai` 不生效）。`≈ todo.md #26`

- [ ] **O3. `ui/` 整目录为 upstream 死重**
  - CI/docker 均不构建；含 1kHz busy-loop（`recv_timeout(Duration::from_nanos(1))`，`ui/src/usecase/presenter.rs`、`ui/src/adapter/view/desktop.rs:121`）和大量 `unwrap()` panic 面。
  - 按项目约束"过时的直接删"——建议整体移除或 README 明确标注不维护。

- [ ] **O4. `src/relay_server.rs` WebSocket 路径多余复制**
  - `bytes[..].into()` / `bytes.to_vec()`（代码自带 "poor performance" to-do 注释），流量大时是热点。

- [ ] **O5. Rust 侧死代码清理**
  - `src/rendezvous_server.rs:1572` `send_rk_res` 死函数；`database.rs` 注释掉的 `PgPoolOptions`/`Sink` 分支等应清理。

- [ ] **O6. `src/utils.rs` doctor 子命令 `lookup_addr/lookup_host().unwrap()`**
  - DNS 失败即 panic，改返回 anyhow error。

- [ ] **O7. `src/common.rs` `check_software_update` 常驻上报线程**
  - 每天向 rustdesk 官方上报版本检查，自托管隐私敏感，加 env 开关或移除。`≈ todo.md #18`

- [ ] **O8. web 控制台登出残留 token**
  - `web/src/utils/auth.js` `removeToken()` 不清 `wc-option:local:access_token`，登出后 webclient 副本 token 残留 localStorage；token 存 localStorage 本身有 XSS 面（upstream 模式，安全文档注明即可）。

---

## 审查通过（无问题）

- `a6f4dfd` 的 4 个 upstream PR backport（`UpdateFields`、`email_verified RawMessage`、`exp=604800*time.Second`、token-expire 秒数兼容）均正确，无回归。
- hbbs/hbbr 核心链路（密钥生成/共享 `gen_sk`/`-k _`、punch hole/relay 主流程、IP blocker、NAT test、peers 内存+sqlite 双层、ws `X-Real-IP` 提取）与 upstream 一致，除 S3/S4 外未发现问题。
- api 中间件（JwtAuth/RustAuth/AdminPrivilege）逻辑正常；审计/地址簿抽查未发现越权；`serverCmd` 只连 loopback 且 hbbs 侧有 `is_loopback()` 校验。
- Docker 三镜像（含 arm64 zig 交叉编译）与 GHCR 流水线架构合理。
- `src/database.rs` 默认 `MAX_DATABASE_CONNECTIONS=1`（sqlite 合理）。

---

## 与 todo.md 的重叠索引

| 本文档 | todo.md | 主题 |
| ------------ | ------- | ------------------------------------ |
| S1/S2 | #19 | jwt 密钥打印 / MUST_LOGIN 空密钥绕过 |
| S3/S4 | #1 | KeyExchange unwrap/panic 远程 DoS |
| S5 | #3（互补） | 代理信任 / LoginLimiter（两文角度不同，需一并修） |
| S6 | #17（同族） | 弱密码学 fallback（access token / 密码） |
| M2 | #13 | LoginLog.UserTokenId 错列 |
| M3 | #14 | 未保护的类型断言 |
| M4 | #8 | .env / db_v2.sqlite3 追踪残留 |
| O1/O2 | #26 | CI/Dockerfile 构建 |
| O7 | #18 | 版本检查上报线程 |

本文档独有：M1（DDL 拼接）、M5（hbbr PORT 语义）、M6（src/mod.rs）、O3（ui/ 移除）、O4（ws 复制）、O5（死代码）、O6（doctor unwrap）、O8（登出残留 token）。
