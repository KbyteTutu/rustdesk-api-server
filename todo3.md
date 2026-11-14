# 第三轮代码审查报告（2026-11-14）

**范围**：Rust 服务端 `src/`（hbbs/hbbr）、Go API `api/`（重点：本地改动文件：config.go、UpdateFields 模式、admin/{oauth,peer,group,deviceGroup,user}.go、model/oauth.go、service/user.go）、web 控制台样式改动（68efd5b）、Docker/CI（ghcr.yml、api.Dockerfile、docker-compose.yml）。

**方法**：全量阅读自定义/改动代码 + 上游对照（lejianwen/rustdesk-api、rustdesk/rustdesk-server）；vendored 目录（libs/hbb_common、ui/）跳过。

---

## 🔴 高严重度（安全 / 稳定性）

### R1. JWT 密钥泄漏到 stdout — `src/jwt.rs:16`
`println!("secret: {:}", SECRET.to_string());` — 每次生成 token 都把 `RUSTDESK_API_JWT_KEY` 明文打印到日志。容器化部署时日志常被收集转发，等于密钥泄漏。建议直接删除该行。
附带：`SECRET` 为空字符串时仍会签发/校验 token，任何人可伪造。建议 `MUST_LOGIN=Y` 但 key 为空时拒绝启动。

### R2. 未认证远程崩溃（panic = abort）— `src/rendezvous_server.rs:1619, 623-624`
- `get_symetric_key_from_msg` 对 seal open 失败直接 `panic!`（:1619）；
- KeyExchange 分支只检查 `keys.len() != 2`，未检查元素长度：`ex.keys[0].to_vec().try_into().unwrap()`（32 字节）、`ex.keys[1]`（48 字节）（:623-624），错误长度即 unwrap panic；
- `Cargo.toml` release profile 为 `panic = 'abort'` → 任一恶意客户端消息可令整个 hbbs 进程崩溃（未认证 DoS）。
建议：改为优雅错误返回（warn + 断开连接），不 panic。

### R3. MUST_LOGIN fail-open — `src/rendezvous_server.rs:910`
`else if !jwt::SECRET.is_empty()` 才验证 token —— JWT key 未配置时，任何非空 token 都通过登录检查。建议 fail-closed：MUST_LOGIN=Y 且 key 为空时直接拒绝并在启动时报错（与 R1 启动检查合并）。

### R4. hbbr 的 `.env` 加载与 hbbs 不一致 — `src/hbbr.rs:28`
hbbr 用原始大小写 `set_var(k, v)`，hbbs 走 `arg_name()` 大写化（`src/common.rs:67,75,81`）。`.env` 写小写 `key=xxx` 时 hbbr 读 `KEY` 落空 → 中继服务器无鉴权开放（白嫖流量中转）。建议 hbbr 复用 hbbs 的 env 加载逻辑。

### G1. OIDC 绑定缺全局唯一性校验（账号混淆）— `api/http/controller/admin/oauth.go:90 BindConfirm`
`BindConfirm` 把缓存 openid 直接绑到当前用户：既没查 openid 是否已被其他用户绑定（`OauthService.UserThirdInfo(op, openid)` 全局查重，`OauthCallback` bind 分支查了），也没查当前用户是否已绑过同 op 账号。重复绑定后 `InfoByOauthId` 用 `First()` 取人 → 登录账号不确定。建议补两个查重，冲突返回 `OauthHasBindOtherUser`。

### G2. `AutoRegister` nil 指针解引用 — `api/http/controller/api/ouath.go:227`
`!*oauthConfig.AutoRegister` — `AutoRegister` 是 `*bool` 且 `FormatOauthInfo()`（`api/model/oauth.go:75`）只归一化 `PkceEnable`/`PkceMethod`，没归一化 `AutoRegister`。上游旧库该列为 NULL（无 gorm default）→ OAuth 登录回调 panic（gin Recovery 兜成 500）。建议 `FormatOauthInfo` 补归一化，或读路径统一格式化。

---

## 🟡 中严重度（bug / 健壮性）

### G3. LoginLog 关联字段存错 — `api/service/user.go:107`
`llog.UserTokenId = ut.UserId` 应为 `ut.Id`。当前把 userId 塞进 UserTokenId，`login_log.user_token_id` 全部失真（上游遗留 bug）。

### G4. 死判断导致无效 token 生成 — `api/http/controller/api/ouath.go:103`
`if u == nil` 永假（`UserService.InfoById` 总返回 `&model.User{}`）。state 有效期内用户被删除时 `u.Id == 0` 照常 `Login()` → 生成 `user_id=0` 废 token 返回给客户端。应改 `if u.Id == 0`。

### G5. UpdateFields 白名单无唯一性校验 — `api/http/controller/admin/user.go:124`、`admin/oauth.go:251`
- user Update 的字段含 `"username"`：改名不查重 → 重名用户，登录 `InfoByUsername` 用 `First()` 取错账号。Create 有 `IsUsernameExists`，Update 没有。
- oauth Update 字段含 `"op"`：可与其他 provider 的 op 撞名（Create 有查重，Update 没有）。
建议两处 Update 前补唯一性校验。

### G6. HTTP 客户端无超时 — `api/service/oauth.go` `getHTTPClientWithProxy`
代理关闭时返回 `http.DefaultClient`（无 timeout）——OIDC discovery/userinfo 请求可能永久挂起。注释写 30s 实际 60s。统一返回 `&http.Client{Timeout: 60s}`。

### G7. 验证码可反复枚举 — `api/utils/login_limiter.go:146 VerifyCaptcha`
答案错误不销毁验证码，有效期内可对同一张图无限重试（4 位 ≈ 上万组合）。建议错误也 `delete` 或按 id 限重试次数。

### G8. 可伪造 IP 的信任面（部署敏感）
- Go：gin 默认信任所有 proxy（`api/http/http.go:20` WARNING）——不配 `Gin.TrustProxy` 时 `c.ClientIP()` 可被 `X-Forwarded-For` 伪造，绕过登录限流/封禁（换 IP = 全新桶）。DEPLOY.md 应强调直连部署设 `trust-proxy: []`。
- Rust：hbbs/hbbr ws 回调无条件采信 `X-Real-IP`（`src/rendezvous_server.rs:1352`、`src/relay_server.rs:405`）——21118/21119 直连可伪造 IP，绕过 IP blocker、污染 peer 注册。建议加开关（仅可信反代后启用）。

### G9. oauth admin List/Detail 回显 `client_secret`
`model.Oauth.ClientSecret` json 序列化输出给前端。建议脱敏（`json:"-"` 或 mask）。

### D1. 默认部署不强制 key — `docker-compose.yml` hbbs `CMD ["-r","hbbr:21117","-k","_"]`
`-k _` 只保证密钥对存在并启用加密，不要求客户端出示 key。README 的"内置 key exchange"是 1.4.7+ 登录所需，另一回事，易混淆。DEPLOY.md 增加"强制 key：`-k <id_ed25519.pub 内容>` + 客户端填 key"说明。

---

## 🟢 低严重度 / 优化建议

### Rust
- `src/mod.rs` 死文件：无引用且 `mod sled_async;` 指向不存在文件，删除。
- `IP_BLOCKER` / `IP_CHANGES` 仅管理员命令手动 retain，长期运行无界增长；建议定时清理（如 24h）。
- `src/database.rs`：`SqliteConnectOptions::from_str(...).unwrap()` 建池路径 panic（坏 URL → abort 崩溃循环）；报错退出更稳。
- `src/peer.rs update_pk`：同 guid 并发首次注册双双 insert，撞 unique index 返 SERVER_ERROR；改 `ON CONFLICT` 或重查。
- `src/common.rs check_software_update`：每次新建 tokio runtime + reqwest client；可复用。
- `gen_sk` 私钥文件建议 `chmod 600`（现 0644）。
- `tests/jwt_tests.rs` 用 `set_var` 驱动 Lazy static，多线程测试有初始化竞态（当前同 key 无害）。
- `src/common.rs:125` fatal 用 println 而非 logger，不一致。

### Go
- `GenerateToken` MD5 fallback（`api/service/user.go`）：key 未配置时 `Md5(username + time.Now().String())`，建议改 `utils.RandomString`（crypto/rand）。
- `getUserCount()`（`service/user.go:476`）无调用方，删除。
- `RegisterByOauth` 的 `tx.Create(ut)` 失败未回滚事务。
- 注册表单 `ConfirmPassword` 只 required 不比较（`admin/user.go Register`）；密码策略 `gte=4` 偏弱，建议可配置最小长度。
- `admin/user.go Current` 的 `token.(string)` 建议带 ok 断言。
- `Paginate` 无 pageSize 上限（99999 可拉全表）；建议 clamp（如 200）。
- `Message` handler（`api/ouath.go:277`）把 query 参数拼进 JS 字符串——值受 i18n 表约束风险低，但建议改 JSON 输出。
- `OidcAuth` 给 github/linuxdo 也发 nonce（无害，上游行为）。

### Web（样式改动，无功能性问题）
- 深色模式未覆盖 `--el-color-primary-light-8/9`，暗色下按钮 hover/边框发白；补一组 `html.dark` 变量。
- 外链 `target="_blank"` 带 `rel="noopener noreferrer"`（login.vue 两处）。

### Docker / CI
- `docker/healthcheck.sh` 引用 s6 路径但无引用（上游残留），删除。
- `api.Dockerfile`：`npm install` → `npm ci`；`go mod tidy` 构建期执行可能改动依赖树，删除或换 `go mod download`。
- Rust 容器以 root 运行且挂载 `./data:/root`（hbbr 容器可读 hbbs 私钥）；建议非 root USER + 分离数据目录。
- CI cargo build 无缓存（每次全量编译）；加 `Swatinem/rust-cache` 或 cache mount。
- compose `RUSTDESK_API_RUSTDESK_API_SERVER` 默认 http——compose 注释里点一句反代 + https，避免裸 HTTP 传 api-token。

---

## ✅ 值得肯定的改动（本轮确认无问题）

- **config.go 默认值 + 裸秒数→Duration 修复**（含 config_test.go、duration_test.go）：解决旧配置缺键登录挂掉、`token-expire: 360000` 解码为纳秒两个真实坑；default/file/env 优先级测试覆盖完整。
- **`UpdateFields`**（`api/service/service.go:73`）：admin "清空字段"从不可能变为可控（白名单含零值更新），注释清楚说明与客户端 API 的行为分界。
- **`OidcUser.VerifiedEmail` 改 `json.RawMessage`**（`api/model/oauth.go` + oauth_test.go）：兼容 Cognito 等违反 spec 的 provider，7 个边界用例。
- **`UserTokenExpireTimestamp` 的 `604800 * time.Second` 修复**（`api/service/user.go:494`）+ user_token_test.go。
- web 控制台纯样式/品牌化改动，无逻辑风险。

## 修复优先级建议

1. R2（远程崩溃）+ R1/R3/R4（密钥泄漏、fail-open、开放中继）——无人即可利用或一条配置错误就裸奔；
2. G1/G2（oauth 绑定/回调）——登录正确性与账号安全；
3. G3/G4/G5 ——正确性 bug，顺手可修；
4. 其余按需排期。
