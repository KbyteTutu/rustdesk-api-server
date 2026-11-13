# 项目审查与改进清单 (TODO)

本文档记录对 `rustdesk-api-server`（`hbbs` / `hbbr` Rust 核心服务、`api/` Go 服务端、`web/` Vue 3 控制台、Docker 部署与 CI/CD）全面代码审查后发现的问题与改进建议。

遵循项目核心约束：
- **信息安全是设计底线**：宁愿牺牲部分性能也要保证信息安全。
- **不保留向后兼容**：过时的直接删，不加兼容层、不留 fallback。
- **最小简洁实现 & 架构往长远做**：用成熟模式解决根本问题，不接受临时拼凑方案。

---

## P0：高危安全与稳定性缺陷（优先修复）

- [ ] **1. 修复 `hbbs` 密钥协商 (`KeyExchange`) 中的 `unwrap()` 与 `panic!` 远程崩溃漏洞 (Remote DoS)**
  - **位置**：
    - `src/rendezvous_server.rs:613-614` (`handle_tcp` -> `Union::KeyExchange(ex)`)
    - `src/rendezvous_server.rs:1605-1622` (`get_symetric_key_from_msg`)
    - `Cargo.toml:69` (`[profile.release]` 配置了 `panic = 'abort'`)
  - **现象与风险**：
    - `ex.keys[0].to_vec().try_into().unwrap()` 与 `ex.keys[1].to_vec().try_into().unwrap()` 未校验公钥（32 字节）和密文盒（48 字节）长度，畸形报文直接触发 panic。
    - `get_symetric_key_from_msg` 中 `box_::open(...)` 解密失败时直接执行 `panic!("Error while opening the seal key{:?}", e)`。任何配置了错误公钥的客户端连接或攻击者发送单条伪造 `KeyExchange` TCP 报文，都会导致整个 `hbbs` 进程直接 `abort` 退出。
  - **改法**：
    - 将 `get_symetric_key_from_msg` 返回值改为 `Result<Vec<u8>, ()>` 或 `Option<Vec<u8>>`。
    - 使用 `.try_into().ok()` 安全校验 `ex.keys[0]` 和 `ex.keys[1]` 长度。
    - 解密失败或长度不合法时仅记录 `log::warn!/debug!` 并关闭当前 TCP 连接（`return false;`），严禁 panic。

- [ ] **2. 修复 `/api/admin/rustdesk/*` 服务端控制指令接口缺失管理员权限校验（越权访问）**
  - **位置**：
    - `api/http/router/admin.go:57-64` (`RustdeskCmdBind`)
    - `api/http/controller/admin/rustdesk.go:79-99` (`CmdUpdate`)
  - **现象与风险**：
    - `RustdeskCmdBind(arg)` 被挂载在仅通过 `middleware.BackendUserAuth()` 的路由组 `arg` 下，漏掉了 `middleware.AdminPrivilege()`。任何普通登录用户均可调用 `/api/admin/rustdesk/sendCmd`、`/cmdList`、`/cmdCreate`、`/cmdDelete` 查看或向 `hbbs`/`hbbr` 发送控制指令（篡改中继列表、黑名单、`must-login` 等）。
    - 控制器已实现 `CmdUpdate`（且前端 `web/src/views/rustdesk/control.vue:246` 已调用 `/admin/rustdesk/cmdUpdate`），但路由层未注册该路径。
  - **改法**：
    - 将 `RustdeskCmdBind` 移入挂载了 `middleware.AdminPrivilege()` 的管理员路由组 `ag` 下。
    - 在 `RustdeskCmdBind` 中补齐 `rg.POST("/cmdUpdate", cont.Rustdesk.CmdUpdate)`。

- [ ] **3. 修复 `LoginLimiter` 防爆破机制失效（封禁变放行、客户端接口未拦截、验证码可重放）**
  - **位置**：
    - `api/http/controller/admin/login.go:36`
    - `api/http/controller/api/login.go:33-65`
    - `api/utils/login_limiter.go:155-248`
  - **现象与风险**：
    - **触发封禁后反而绕过验证码与封禁**：当某 IP 失败次数达到 `BanThreshold` 时，`banIP()`（`login_limiter.go:240-247`）将该 IP 写入 `bannedIPs` 并执行 `delete(ll.attempts, ip)`。此后 `CheckSecurityStatus(ip)` 返回 `(banned: true, captchaRequired: false)`。但 `admin/login.go:36` 写成了 `_, needCaptcha := loginLimiter.CheckSecurityStatus(clientIp)`，丢弃了 `banned` 返回值，导致攻击者达到封禁阈值后不仅未被拒绝，连 `needCaptcha` 也变成了 `false`。
    - **客户端登录接口 `/api/login` 无拦截**：`api/http/controller/api/login.go` 仅在密码错误时调用 `RecordFailedAttempt`，既未在校验前调用 `CheckSecurityStatus` 拦截封禁 IP，也未在登录成功后调用 `RemoveAttempts`。
    - **验证码未一次性销毁**：`VerifyCaptcha`（`login_limiter.go:177`）仅在答案正确时 `delete(ll.captchas, id)`，答案错误时不销毁该 `captchaId`，攻击者可对同一 `captchaId` 持续穷举。
  - **改法**：
    - 在 `admin/login.go` 与 `api/login.go` 入口统一检查 `banned, needCaptcha := loginLimiter.CheckSecurityStatus(clientIp)`，若 `banned` 为真直接返回封禁错误。
    - 在 `api/login.go` 登录成功后调用 `loginLimiter.RemoveAttempts(clientIp)`。
    - 在 `VerifyCaptcha` 中无论比对成功与否，校验一次后立即 `delete(ll.captchas, id)`。

- [ ] **4. 修复 LDAP 过滤器注入漏洞与 BaseDN 错误转义**
  - **位置**：`api/service/ldap.go:183, 305, 313, 381-383, 447, 501`
  - **现象与风险**：
    - `filterField(field, value)`（`api/service/ldap.go:381`）直接执行 `fmt.Sprintf("(%s=%s)", field, value)`，未对用户输入的 `username` / `email` 调用 `ldap.EscapeFilter(value)`，存在 LDAP 注入漏洞。
    - 反过来，在 `isUserInGroup`（183 行）、`isUserAdmin`（447 行）、`getAttrOfDn`（501 行）中，代码对 `ldap.NewSearchRequest` 的 **BaseDN** 参数调用了 `ldap.EscapeFilter(...)`，导致包含特殊字符的合法 DN 查询失败。
  - **改法**：
    - 在 `filterField` 中使用 `fmt.Sprintf("(%s=%s)", field, ldap.EscapeFilter(value))`。
    - 移除 `ldap.NewSearchRequest` 的 BaseDN 参数上的 `ldap.EscapeFilter` 调用。

- [ ] **5. 修复 OIDC / OAuth 登录未校验用户禁用状态 (`Status`)**
  - **位置**：`api/http/controller/api/ouath.go:79-115, 220-250`（顺带修正文件名拼写 `ouath.go` -> `oauth.go`）
  - **现象与风险**：
    - `OauthCallback` 和 `OidcAuthQueryPre` 查出本地用户 `u` 后，未调用 `service.AllService.UserService.CheckUserEnable(u)` 即直接签发登录 Token，导致被管理员禁用的账号仍可通过第三方 OIDC/OAuth 登录。
  - **改法**：
    - 在 `OauthCallback` 和 `OidcAuthQueryPre` 签发 Token 前统一校验 `!service.AllService.UserService.CheckUserEnable(u)`，若被禁用则拒绝登录。

---

## P1：架构、容器化部署与核心功能缺陷

- [ ] **6. 打通三容器架构下的 `ServerCmd` 控制指令通道与中继参数配置**
  - **位置**：
    - `api/service/serverCmd.go:40-80`
    - `api/conf/config.yaml:17-18`
    - `src/rendezvous_server.rs:1281-1293`、`src/relay_server.rs:365-377`
    - `docker/hbbs.Dockerfile:12`、`docker/hbbr.Dockerfile:11`、`docker-compose.yml:6-64`
  - **现象与风险**：
    1. **后台 ServerCmd 不可用**：`api/service/serverCmd.go` 硬编码向 `127.0.0.1` / `[::1]` 发送 UDP/TCP 指令，且 `api/conf/config.yaml:17` 将 `id-server-port` 误配为 `21116`（`hbbs` 实际在 `21115` 监听控制指令）。在 `docker-compose.yml` 三容器拆分架构下，`api` 容器访问自身的 `127.0.0.1` 无法到达 `hbbs`/`hbbr`；且 `hbbs`/`hbbr` 源码限制了 `if ip.is_loopback()` 才处理控制指令。
    2. **`hbbs` 默认下发内网容器名 `hbbr:21117`**：`docker/hbbs.Dockerfile:12` 默认 `CMD ["-r", "hbbr:21117", "-k", "_"]`，导致 `hbbs` 将容器内网主机名 `"hbbr:21117"` 下发给外部客户端，外部客户端无法解析 `hbbr`。
    3. **`hbbr` 未开启 `-k _` 校验且挂载权限过宽**：`docker/hbbr.Dockerfile` 未传 `-k _`，且 `docker-compose.yml:34` 将 `./data:/root` 以读写模式挂载给 `hbbr`。
  - **改法**：
    - `docker-compose.yml` 中 `hbbs` 的命令使用 `${RUSTDESK_HOST}:21117`；`hbbr` 增加 `command: ["-k", "_"]` 且将 `./data:/root:ro` 设为只读挂载。
    - 支持通过配置指定 `hbbs`/`hbbr` 控制端地址（默认 `21115` / `21117`），并在 `hbbs`/`hbbr` 控制端口支持容器内网或受控地址访问。

- [ ] **7. 修复首次启动时 `api` 读取 `id_ed25519.pub` 的竞态条件**
  - **位置**：`api/config/rustdesk.go:26-39`、`api/config/config.go:103`、`docker-compose.yml:40-64`
  - **现象与风险**：
    - `LoadKeyFile()` 仅在 `api` 进程启动时执行一次，且未做 `strings.TrimSpace`，读取失败时静默忽略。
    - `docker-compose.yml` 中 `api` 仅配置了 `depends_on: - hbbs`（未等待 `service_healthy`）。首次启动时若 `api` 先于 `hbbs` 生成密钥前读取 `/data/id_ed25519.pub`，`Rustdesk.Key` 将一直为空，直到手动重启 `api`。
  - **改法**：
    - `docker-compose.yml` 中 `api` 的 `depends_on` 改为等待 `hbbs` 的 `condition: service_healthy`。
    - `LoadKeyFile()` 对读取内容执行 `strings.TrimSpace`；当 `Rustdesk.Key` 为空且 `KeyFile` 非空时，在 `/api/admin/config/server` 等读取处支持按需懒加载重试。

- [ ] **8. 解耦 `.env` 编译期配置与运行期部署配置，清理工作区 `db_v2.sqlite3`**
  - **位置**：`.env`、`.env.example`、`.gitignore`、`.cargo/config.toml`、`db_v2.sqlite3`
  - **现象与风险**：
    - `.env` 被 Git 跟踪且仅包含 `DATABASE_URL=sqlite://./db_v2.sqlite3`（供 Rust `sqlx::query!` 宏编译期使用）。
    - 用户按 `README.md`/`DEPLOY.md` 执行 `cp .env.example .env` 会弄脏 Git 工作树并覆盖掉 `DATABASE_URL` 导致 `cargo build` 失败；若用户不执行 `cp`，Docker Compose 则会读取已跟踪的 `.env`，导致 `RUSTDESK_HOST` 缺失并静默回退为 `example.com`。
  - **改法**：
    - 将 `DATABASE_URL = "sqlite://./db_v2.sqlite3"` 移入 `.cargo/config.toml` 的 `[env]` 段（或生成 SQLx 离线元数据 `.sqlx`）。
    - 执行 `git rm --cached .env` 并将 `.env` 加入 `.gitignore`。

- [ ] **9. 修正 `DEPLOY.md` 中的版本号与本地构建命令错误**
  - **位置**：`DEPLOY.md:5, 121-130, 146-147, 171`
  - **现象与风险**：
    - `DEPLOY.md:146-147` 指导本地构建时拷贝到 `docker/bin/stage/amd64/`，但 `docker/hbbs.Dockerfile:5` 与 `docker/hbbr.Dockerfile:5` 实际为 `COPY docker/bin/${TARGETARCH}/...`（无 `stage/` 层级），按文档操作必败。
    - `DEPLOY.md` 多处仍残留 `0.1` / `0.1.0` 描述，与当前 `0.2` 基线不一致。
  - **改法**：统一修正为 `docker/bin/amd64/` 与 `0.2` 版本描述。

---

## P1/P2：后端业务逻辑与数据一致性缺陷 (`api/`)

- [ ] **10. 修复未认证客户端接口 `/api/sysinfo` 与 `/api/heartbeat` 跨设备覆盖 `Peer` 信息**
  - **位置**：
    - `api/http/controller/api/peer.go:25-60` (`SysInfo`)
    - `api/http/controller/api/index.go:38-61` (`Heartbeat`)
  - **现象与风险**：
    - 两个接口均为未认证接口。当根据 `f.Id` 查到已有设备记录 `pe.RowId != 0` 时，代码未校验请求的 `f.Uuid == pe.Uuid`，直接用请求参数覆盖了 `pe` 的 `Uuid`、`Hostname`、`Username`、`Os`、`Version`、`Cpu` 等字段。任何获知 9 位设备 ID 的调用方均可伪造或篡改设备元数据与在线状态。
  - **改法**：当数据库中已存在该 `Id` 且 `pe.Uuid != ""` 时，校验 `pe.Uuid == f.Uuid`，不一致时拒绝更新并记录警告日志。

- [ ] **11. 修复旧版地址簿全量同步接口 `POST /api/ab` 误删非默认地址簿名称 (`collection_id > 0`) 数据**
  - **位置**：`api/service/addressBook.go:53-98` (`UpdateAddressBook`)
  - **现象与风险**：
    - `tx.Where("user_id = ?", userId).Find(&dbABs)` 未限制 `collection_id = 0`，对比差集后会将该用户在其他地址簿名称（`collection_id > 0`）下的所有设备全部 `tx.Delete(dbAB)` 误删。
    - 事务内的 `tx.Create`、`tx.Updates`、`tx.Delete` 均未检查 `err` 并执行 `tx.Rollback()`。
  - **改法**：查询条件增加 `and collection_id = 0`，并对事务中每一步数据库操作做错误检查与回滚。

- [ ] **12. 修复设备列表按 `uuids` 批量过滤时的 SQL `IN` 参数错误**
  - **位置**：
    - `api/http/controller/admin/peer.go:99-101`
    - `api/http/controller/admin/my/peer.go:47-49`
  - **现象与风险**：`tx.Where("uuid in (?)", query.Uuids)` 直接传入了逗号分隔的字符串 `query.Uuids`，未调用 `strings.Split(query.Uuids, ",")`，导致 GORM 将整串当作单个元素查询，批量查询失效。
  - **改法**：改为 `tx.Where("uuid in (?)", strings.Split(query.Uuids, ","))`。

- [ ] **13. 修复登录日志 `LoginLog.UserTokenId` 字段赋值错误**
  - **位置**：`api/service/user.go:122`
  - **现象与风险**：`llog.UserTokenId = ut.UserId` 误将用户 ID 赋给了 `UserTokenId`，导致删除登录日志联动吊销 Token 时无法匹配到正确的 `UserToken.Id`。
  - **改法**：改为 `llog.UserTokenId = ut.Id`。

- [ ] **14. 修复多处未保护的类型断言、切片越界与空指针解引用 (Panic 隐患)**
  - **位置**：
    - `api/http/controller/api/ab.go:697`：`fidstr := fid.(string)` 未做 `ok` 检查，若 JSON 中 `id` 非字符串直接 panic。
    - `api/http/middleware/rustauth.go:25-27`：`token = token[7:]` 未校验 `strings.HasPrefix(token, "Bearer ")`。
    - `api/service/user.go:286`：`IsAdmin` 中 `return u != nil && *u.IsAdmin` 未检查 `u.IsAdmin != nil`。
  - **改法**：补齐类型断言 `fidstr, ok := fid.(string)`、`strings.CutPrefix(token, "Bearer ")` 以及 `u != nil && u.IsAdmin != nil && *u.IsAdmin`。

- [ ] **15. 修复普通用户创建标签未校验 `CollectionId` 归属权**
  - **位置**：`api/http/controller/admin/my/tag.go:59-80` (`Create`)
  - **现象与风险**：`Update`（112 行）校验了 `CheckCollectionOwner(u.Id, f.CollectionId)`，但 `Create` 未校验，普通用户可指定他人的 `collection_id` 创建标签。
  - **改法**：当 `f.CollectionId > 0` 时，在 `Create` 中同样校验 `!service.AllService.AddressBookService.CheckCollectionOwner(u.Id, f.CollectionId)`。

- [ ] **16. 收紧 `/api/admin/user/groupUsers` 与 `/api/admin/oauth/info` 的敏感字段暴露**
  - **位置**：
    - `api/http/router/admin.go:82`、`api/http/controller/admin/user.go:275-283` (`GroupUsers`)
    - `api/http/controller/admin/oauth.go:18-30` (`Oauth.Info`)
  - **现象与风险**：
    - `/api/admin/user/groupUsers` 允许任意普通登录用户拉取全站最多 9999 名用户的完整 `model.User` 信息（含邮箱、`is_admin`、状态、备注等）。
    - `/api/admin/oauth/info` 直接将 `OauthCacheItem` 原始结构返回前端（含 PKCE `Verifier`、`Nonce` 等内部字段）。
  - **改法**：`GroupUsers` 仅返回必要的脱敏字段（`id`, `username`, `nickname`, `group_id`）；`Oauth.Info` 仅返回前端展示所需的视图字段（`op`, `third_name`, `device_name`, `id`）。

- [ ] **17. 移除过时的 MD5 密码校验 fallback 与无条件 Redis 客户端初始化，强化初始管理员随机密码**
  - **位置**：
    - `api/utils/password.go:25-35`
    - `api/cmd/apimain.go:125-130, 349-350`
    - `api/utils/tools.go:34-44`
  - **现象与风险**：
    - `VerifyPassword` 在 bcrypt 校验失败后仍回退到 `Md5(input + "rustdesk-api")`，违反项目约束 #1（不保留向后兼容与 fallback）及安全底线。
    - `InitGlobal()` 在未启用 Redis 缓存时仍无条件执行 `redis.NewClient`。
    - `utils.RandomString` 使用 `math/rand`（非密码学安全随机数）生成初始管理员密码。
  - **改法**：
    - 删除 `api/utils/password.go` 中的 MD5 fallback 分支及 `Md5()` 函数。
    - 仅在 `config.Cache.Type == config.TypeRedis` 时初始化 `global.Redis`。
    - 使用 `crypto/rand` 替换 `utils.RandomString` 的实现。

---

## P2：Rust 核心服务 (`hbbs` / `hbbr`) 改进项

- [ ] **18. 移除 `hbbs` 后台向 `rustdesk.com` 发起的软件版本检查上报线程**
  - **位置**：`src/main.rs:78`、`src/common.rs:188-211` (`check_software_update`)
  - **现象与风险**：`hbbs` 启动后会开启后台线程每 24 小时向 `https://rustdesk.com/version_check_request` 发送 POST 请求，在私有化部署场景下存在不必要的外联与信息泄露。
  - **改法**：直接删除 `check_software_update` 及其调用。

- [ ] **19. 修复 `src/jwt.rs` 密钥打印泄露与 `MUST_LOGIN` 空密钥绕过漏洞**
  - **位置**：`src/jwt.rs:15`、`src/rendezvous_server.rs:909-915`
  - **现象与风险**：
    - `src/jwt.rs:15` 中残留 `println!("secret: {:}", SECRET.to_string());`。
    - `src/rendezvous_server.rs:909`：当 `MUST_LOGIN=Y` 开启但环境变量未配置 `RUSTDESK_API_JWT_KEY`（`jwt::SECRET.is_empty()`）时，`else if !jwt::SECRET.is_empty()` 被跳过，客户端传入任意非空字符串 `token` 即可绕过登录鉴权。
  - **改法**：删除 `println!`；当 `MUST_LOGIN` 开启而 `jwt::SECRET.is_empty()` 时，直接拒绝请求并输出错误日志提示配置 `RUSTDESK_API_JWT_KEY`。

- [ ] **20. 收紧 `id_ed25519` 私钥生成文件权限为 `0600`**
  - **位置**：`src/common.rs:117` (`gen_sk`)
  - **现象与风险**：`std::fs::File::create(sk_file)` 使用默认 umask 创建私钥文件（通常为 `0644` 全局可读）。
  - **改法**：在 Unix 平台下使用 `std::os::unix::fs::OpenOptionsExt::mode(0o600)` 创建私钥文件。

- [ ] **21. 修复 WebSocket 连接上加密收发不对称问题**
  - **位置**：`src/rendezvous_server.rs:86, 1330-1350`
  - **现象与风险**：`Sink::Wss` 发送消息时会使用 `KeyExchange` 协商的 `encrypt` 进行加密，但在 `handle_listener_inner` 的 `ws` 接收分支中既未调用 `key_exchange_phase1`，也未对收到的帧执行解密。
  - **改法**：对齐 TCP 与 WebSocket 分支的握手与帧加解密逻辑，或明确限制 WebSocket 端由 TLS (`wss://`) 承载而不在应用层启用半套对称加密。

- [ ] **22. 为 `PeerMap` 内存缓存增加淘汰策略并优化 SQLite 连接配置**
  - **位置**：`src/peer.rs:64`、`src/database.rs:50-65`
  - **现象与风险**：
    - `PeerMap.map` (`HashMap<String, LockPeer>`) 永不清理离线设备，长期运行下内存单调增长。
    - `src/database.rs` 中 SQLite未开启 `WAL` 模式与 `busy_timeout`，默认连接数为 1，高并发下易发生锁争用。
  - **改法**：定期清理长时间未活跃的内存 Peer 条目；SQLite 初始化时开启 `journal_mode=WAL` 与 `busy_timeout=5000`。

---

## P2：前端控制台 (`web/`) 改进项

- [ ] **23. 修复浅色主题下顶栏右侧文字/图标纯白不可见的样式 Bug**
  - **位置**：`web/src/layout/components/setting/index.vue:85`
  - **现象与风险**：commit `68efd5b` 将顶栏改为浅色 `var(--surface)`（`#ffffff`），但 `setting/index.vue:85` 的 `.title` 仍硬编码 `color: #fff;`，导致多语言切换图标、当前用户名及下拉箭头在白色顶栏上完全看不见。
  - **改法**：将 `color: #fff;` 改为 `color: var(--text);`。

- [ ] **24. 修复 OAuth 授权/绑定路由守卫白名单与页面主题适配**
  - **位置**：`web/src/permission.js:13`、`web/src/views/oauth/bind.vue`、`web/src/views/oauth/login.vue`
  - **现象与风险**：
    - `oauth/bind.vue` 与 `oauth/login.vue` 仍硬编码旧版深色背景 `#2d3a4b` / `#283342`，未适配新版设计语言变量（`var(--bg)`、`var(--surface)`）。
    - 当未登录用户通过 OAuth 回调跳转到 `/_admin/#/oauth/bind/:code` 时，被 `permission.js` 拦截跳转到 `/login?redirect=/oauth/bind/:code`，需确保登录完成后能平滑回到绑定确认页。
  - **改法**：统一 `oauth/bind.vue` 与 `oauth/login.vue` 的卡片样式与 CSS 变量，验证并完善 `redirect` 回跳链路。

- [ ] **25. 清理前端冗余依赖与模板残留代码**
  - **位置**：`web/package.json`、`web/vite.config.js:8-12, 51`、`web/src/layout/components/setting/index.vue:50`
  - **现象与风险**：
    - `web/package.json` 同时依赖已废弃的 `@element-plus/icons` (`^0.0.11`) 与 `@element-plus/icons-vue` (`^2.0.10`)，且包含 Vite 无需使用的 `sass-loader`、`dotenv`。
    - `web/vite.config.js:51` 残留上游模板路径判断 `else if(id.includes('Gwen-admin/src'))`。
  - **改法**：将 `setting/index.vue:50` 的 `@element-plus/icons` 统一替换为 `@element-plus/icons-vue`，移除 `@element-plus/icons`、`sass-loader`、`dotenv` 及 `vite.config.js` 中的死代码。

---

## P2：CI/CD 与构建流水线改进项

- [ ] **26. 优化 `docker/api.Dockerfile` 与 `.github/workflows/ghcr.yml` 构建流水线**
  - **位置**：`docker/api.Dockerfile:1-39`、`.github/workflows/ghcr.yml:1-99`
  - **现象与风险**：
    - `docker/api.Dockerfile` 使用 `npm install`（而非锁版本的 `npm ci`）和构建期 `go mod tidy`（而非 `go mod download`），且 `go build` 未开启 `-trimpath -ldflags="-s -w"`。
    - `.github/workflows/ghcr.yml` 中 `api` 镜像构建无需依赖 `build-rust` 产物，却被 `needs: build-rust` 阻塞等待多架构 Rust 交叉编译；且缺少日常 PR/分支推送的编译与单测 CI 校验。
  - **改法**：
    - `docker/api.Dockerfile` 改为 `npm ci`、`go mod download` 与 `go build -trimpath -ldflags="-s -w"`。
    - `ghcr.yml` 增加 Cargo 缓存，解耦 `api` 与 `hbbs`/`hbbr` 镜像构建依赖，并补充日常推送的快速检查工作流（`cargo check`、`go vet && go test`、`npm run build`）。
