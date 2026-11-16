# 第四轮代码审查报告（2026-11-14）

**范围**：全仓。`api/`（147 个 Go 文件，全部 controller/service/model/lib/中间件/入口）、`src/`（hbbs 的 JWT 与登录准入改动）、`web/`（控制台）、`docker/` 与根 `docker-compose.yml`、构建产物（`docker/bin/*`）。

**方法**：逐文件通读 + 交叉验证（路由注册顺序、中间件链、GORM 事务语义、Rust 与 Go 的 JWT 契约）；实际执行 `go build ./...`、`go test`、`gofmt -l`。**未修改任何代码。**

**与前三轮的关系**：以 todo3.md 为基线。重复项标注「延续 …」并只补充新证据；本轮新增 20 项。

---

## 🔴 阻断 / 高危

### B1. 仓库无法直接构建：`api/go.sum` 不存在
```text
cmd/apimain.go:9:2: missing go.sum entry for module providing package github.com/go-redis/redis/v8
config/config.go:5:2: missing go.sum entry ... github.com/spf13/viper
global/i18n.go:4:2: missing go.sum entry ... github.com/BurntSushi/toml
```
`go build ./...`、`go test ./...` 在干净检出上全部失败。`docker/api.Dockerfile` 里的 `go mod tidy` 正是在掩盖这个问题（顺带使镜像构建不可复现，且构建期会改写 go.mod/go.sum）。
**建议**：提交完整 `go.sum`；构建改 `go mod download && go build`；CI 增加 `go build ./... && go vet ./... && go test ./...`。

### B2. Gin 中间件注册顺序陷阱 → 一批未鉴权写接口
`api/http/router/api.go:76` 的 `frg.Use(middleware.RustAuth())` 只作用于**其后注册**的路由。因此以下端点全部匿名可用：

| 端点 | 位置 | 影响 |
| --- | --- | --- |
| `POST /api/sysinfo` | `api.go:60` | 按请求体 `Id` upsert 设备；`UserId` 由客户端可控的 `uuid/device_id` 经 `FindLatestUserIdFromLoginLogByUuid` 推断 → 伪造设备信息、把设备绑定到任意最近登录用户 |
| `POST /api/sysinfo_ver` | `api.go:61` | 匿名读取服务端版本与启动时间 |
| `POST /api/audit/conn` / `/audit/file` | `api.go:71,73` | 伪造审计记录；`action=new` 无幂等键 → **匿名无限插行**（DB 增长 DoS） |
| `POST /api/heartbeat` | `api.go:41` | 按 `Id` 改任意设备在线状态与 IP |
| `POST /api/shared-peer` | `api.go:141`（`WebClientRoutes` 在 `Use` 之前调用） | `(*j)["share_token"].(string)` 无 comma-ok → 缺字段/数字直接 panic 500；这也是分享链接的入口，需限流 |

**建议**：把公开端点收拢到一个显式 `public` 子分组（不要依赖 `Use` 的时序），sysinfo/heartbeat/audit 改为带共享密钥头或一次性票据；`shared-peer` 加 IP 限流与类型断言保护。

### B3. 文件缓存 100% 失效 — `api/lib/cache/file.go:43-44`
```go
difT := time.Now().Sub(fileInfo.ModTime())
if difT >= 0 { os.Remove(f); return "", nil }
```
刚写入的文件 `difT` 也为正 → **每次 Get 都删文件并返回空**。配置 `cache.type: file` 等于没有缓存。`lib/cache/file_test.go` 只 `fmt.Println` 结果、不校验返回值，正好掩盖了这个 bug。
**建议**：按 `ModTime().Add(ttl).Before(now)` 判断过期；测试补断言。

### B4. LDAP 过滤器注入 — `api/service/ldap.go` `filterField`
`fmt.Sprintf("(%s=%s)", field, value)` 未 `ldap.EscapeFilter`；`value` 来自登录用户名与 OAuth 邮箱 → 可注入 `*)(|(uid=*))` 实现枚举/绕过。`getAttrOfDn` 把 `ldap.EscapeFilter(dn)` 当搜索 base 也是错的（DN 不是 filter）。
**建议**：所有进入过滤器的值统一 `ldap.EscapeFilter`；base DN 只做合法性校验。

### B5. 上传链路：匿名托管 + nil panic + 0777
- `g.StaticFS("/upload", http.Dir(...))`（`api.go` 末尾）直接托管上传目录，`http.Dir` 还开目录列表 → 上传 `.html/.svg` 即同源存储型 XSS/钓鱼页。
- `api/http/controller/admin/file.go`：`file, _ := c.FormFile("file")` 忽略错误 → 无文件时 `file.Filename` nil panic；无扩展名/大小/MIME 校验；`os.MkdirAll(..., os.ModePerm)` = 0777。
**建议**：扩展名白名单 + `http.DetectContentType` + 大小上限；服务端生成文件名；下载响应加 `Content-Disposition: attachment` 与 `X-Content-Type-Options: nosniff`；关闭目录列表。

### B6. OSS 回调 SSRF + 签名伪造 — `api/lib/upload/oss.go:178`
`http.Get(string(publicKeyURL))`，URL 来自请求头 `x-oss-pub-key-url`：无域名白名单、无超时、`ReadAll` 不限长、错误路径不关 Body。可 SSRF 打内网/云元数据、可用攻击者自己的密钥伪造回调签名、可挂住 goroutine。
**建议**：公钥域名固定（或内置公钥）、`http.Client{Timeout: 5s}`、`io.LimitReader`。另：475 行手写 OSS 建议直接换官方 SDK。

### B7. OAuth state 可被任意登录用户抢占 — `api/http/controller/admin/oauth.go` `Confirm`
```go
v := service.AllService.OauthService.GetOauthCache(j.Code)
v.UserId = u.Id
service.AllService.OauthService.SetOauthCache(j.Code, v, 0)
```
任何登录用户拿到别人的 state 就能把该次授权改绑到自己；既不删缓存也不校验 `Action`，5 分钟内可重放；直接修改 `sync.Map` 中的指针（data race）。`Info` 还会把整个 `OauthCacheItem`（含 `Verifier/Nonce/UserId`）返回给任意登录用户。
**建议**：state 一次性消费（先 `Delete` 再处理）、校验 `Action` 与发起方 session、对返回字段脱敏。

### B8. OAuth 账号绑定：`VerifiedEmail` 被忽略 + `AutoRegister` nil（延续 G2，补充根因）
- `api/service/user.go` `RegisterByOauth`：**只要 email 相同就绑定到既有本地账号**；`OauthUser.VerifiedEmail` 解析后从未参与决策（Linuxdo 还硬编码 `VerifiedEmail: true`）→ 预注册账号劫持。
- `model/oauth.go` `FormatOauthInfo` 只归一化 `PkceEnable/PkceMethod`，不兜底 `AutoRegister` → `api/http/controller/api/ouath.go` 的 `if !*oauthConfig.AutoRegister` 会 nil deref panic。回调成功后也不 `Delete` 缓存。
**建议**：`AutoRegister` 模型兜底 + DB `NOT NULL DEFAULT false`；仅 `VerifiedEmail == true` 时才允许邮箱合并，否则一律走绑定页；回调处理完立即删 state。

---

## 🟡 中严重度

### G1. LoginLog 关联字段存错（延续 G3）— `api/service/user.go:107`
`llog.UserTokenId = ut.UserId` 应为 `ut.Id`；`login_log.user_token_id` 全部失真。

### G2. Token 续期单位混用 — `api/service/user.go` `AutoRefreshAccessToken`
`ut.ExpiredAt - now < Config.App.TokenExpire.Milliseconds()/3000`：左边是秒，右边是“毫秒/3000”。恰好约等于 1/3 生命周期；`TokenExpire=0` 时阈值 0 → 永不续期，与 `UserTokenExpireTimestamp` 的 7 天默认不一致。

### G3. `UpdateFields` 绕过“最后管理员”保护 — `api/http/controller/admin/user.go` Update
白名单含 `is_admin`，走 `service.UpdateFields` 而非 `UserService.Update` → 可把最后一个管理员降级/禁用（`UserService.Update` 里的守卫被绕过）。

### G4. 事务错误被吞后无条件 Commit — `api/service/addressBook.go`
`UpdateAddressBook` 全程不看 `tx.Error`，最后 `tx.Commit()` 无条件执行；`DeleteCollection`（:279）同样。部分写入被当成成功。

### G5. `UpdateCollection` 批量赋值 `user_id` — `api/service/addressBook.go:277`
```go
func (s *AddressBookService) UpdateCollection(t *model.AddressBookCollection) error {
	return DB.Model(t).Updates(t).Error
}
```
`my` 控制器（`api/http/controller/admin/my/addressBookCollection.go:85`）只校验 `ex.UserId == u.Id` 就原样回写 → 可把集合所有权转给他人；`Name` 因零值跳过无法清空。

### G6. 地址簿条目可写入他人集合 — `service/addressBook.go` `UpdateAddressBook`
`Updates(ab)` 时 `CollectionId` 来自请求且不校验归属 → 可把条目塞进别人的地址簿（`/api/ab` 会读出来）。`my/tag.go:59 Create` 同样不校验 `CollectionId`。

### G7. `UpdateTags` 清空跨集合标签 — `api/service/tag.go`
先删该用户**全部**标签（不区分 `collection_id`），新建时 `CollectionId` 恒为 0 → RustDesk 客户端同步个人地址簿会清掉其他地址簿的标签；全程忽略错误。

### G8. `Peer.Id` 非唯一 — `api/model/peer.go:5`
`gorm:"default:'';not null;index"`，而 `FindById` 用 `First` → 同 ID 可多行、归属二义。应为 `uniqueIndex`。

### G9. `uuid in (?)` 多值过滤失效 — `api/http/controller/admin/peer.go`、`admin/my/peer.go`
传的是逗号字符串而非切片（文档还写“逗号分隔”）→ 多 UUID 查询永远不命中。应 `strings.Split`。

### G10. 未保护的类型断言 → 500
`api/http/controller/api/ab.go` `PeerUpdate` 的 `fid.(string)`（客户端传数字即 panic）；`admin/file.go` 的 `FormFile`；`admin/user.go Current` 的 `token.(string)`（延续 todo3）。

### G11. Group / DeviceGroup 裸删 — `api/service/group.go:37`
`Delete` 直接 `DB.Delete(u)`：可删 `id=1` 默认组（注册与 LDAP 同步硬编码 `GroupId:1`）和仍有成员的组 → 用户/设备 `group_id` 悬空。

### G12. 删除不级联 — `api/service/user.go` Delete
事务内删 user/UserThird/AB/collection/rule，但**不删 UserToken、LoginLog、ShareRecord**；peer 解绑在事务外且失败只 `Warn` 后返回 nil。

### G13. SQLite 并发与初始化 — `api/lib/orm/sqlite.go`
硬编码 `./data/rustdeskapi.db` 且不建目录；`MaxOpenConns` 默认 100 → 并发写 `database is locked`（无 WAL/busy_timeout）。`mysql.go`/`postgresql.go` 同款 `if err != nil { fmt.Println(err) }` 后继续用 `db` → 连接失败时 nil panic。

### G14. MemoryCache 语义与资源 — `api/lib/cache/memory.go`
覆盖写时 `usedBytes` 只加不减（误淘汰）；`exp<=0` 变 365 天；每实例一个永不停止的 ticker，Handler 无 `Close`；`stopEviction` 二次 close panic。

### G15. LDAP 默认配置不安全 — `api/service/ldap.go`
`isUserEnabled` 读原值 `cfg.User.EnableAttr`（默认字段名是 `userAccountControl`）→ 未显式配置时**所有 LDAP 用户视为启用**（AD 禁用账号可登录）；`Sync=true` 时 `localUser.IsAdmin = &isAdmin` 会把既有本地管理员降级、`Status` 被覆盖；`verifyCredentials` 把所有错误映射为“密码错误”（掩盖宕机）；`Authenticate` 先查用户再验密码（枚举/时序）；`SizeLimit: 0`。

### G16. Rust 登录准入：空密钥放行 + 无归属校验（延续 R3，补第二点）
`src/rendezvous_server.rs`：`MUST_LOGIN=Y` 且 `jwt::SECRET` 为空时任何非空 token 放行；验证通过后也**不校验 token 的 `user_id` 与目标 peer 的归属** → 任意登录用户可与任意设备互连（跨租户）。

### G17. `always-use-relay` 覆盖 relay 列表 — `src/rendezvous_server.rs:1251`
设置 `Y/N` 后额外 `self.tx.send(Data::RelayServers0(rs.to_owned()))`，把 `"Y"` 当 relay 地址解析 → relay 列表被写成 `Y`（复制粘贴 bug）。

### G18. 控制台命令可关闭强制登录 — `api/http/controller/admin/rustdesk.go` SendCmd
把 `cmd + " " + option` 写进本机 21115/21117 控制台端口，可下发 `must-login N`、`ip-blocker`、`relay-servers`；`CmdCreate/CmdUpdate` 直接 bind `model.ServerCmd`（可塞 Id）。建议白名单化并记录审计。

### G19. 密码策略与表单校验（延续）
`ConfirmPassword` 从不与 `Password` 比较；密码仅 `gte=4,lte=32`；`utils/password.go` 保留 `Md5(input+"rustdesk-api")` 兼容分支且用 `==` 比较（非恒定时间）——违反 AGENTS.md #1。

### G20. 死抽象与死代码
`global.Cache` 全仓只写不读（仅 apimain 赋值）；`middleware/jwt.go` 的 `JwtAuth` 无引用且若启用会绕过注销；`middleware/cors.go` 未被使用且实现为反射任意 Origin + `Allow-Credentials: true`；`lib/cache/redis.go` 的 `New()` 留下 `rdb == nil` 分支。

---

## 🟢 低严重度 / 优化

### Go
- `global/i18n.go`：`fileInfo.Name()[len(...)-5:]` 对短文件名 panic；`LoadMessageFile` 错误被忽略。
- `global/logger.go`：日志文件 0644、无轮转；`./runtime` 不存在时启动 panic。
- `Paginate` 无 pageSize 上限（延续），可被放大成全表导出。
- JWT 未限定算法（建议 `jwt.WithValidMethods([]string{"HS256"})`）；无 `jti/iat` → 同一用户同一秒登录得到完全相同的 token；key 为空时退化为 `Md5(username+time.Now().String())`，应换 `crypto/rand`。
- `config/config.go` 的“旧配置秒数→Duration”兼容分支（延续 todo3 认可，但与 AGENTS.md #1 冲突，建议下个大版本删除）。
- 注释与实现不符：`http.go` trust-proxy 的 WARNING 与“默认安全”假设相反；`AutoRefreshAccessToken` 注释“小于1天”；`service.UpdateFields` 注释声称仅 admin 使用。
- `admin/oauth.go` Update 用 `UpdateFields` 未调用 `FormatOauthInfo`；`Oauth.op` 无唯一索引（并发可重复）。

### Web
- `web/src/views/my/info.vue:34`：`v-html="marked(appStore.setting.hello)"`，`marked` 不净化；建议 `DOMPurify.sanitize` 或纯文本渲染。
- token 与 OIDC code 存 `localStorage`（XSS 可窃取）。

### Docker / CI
- `docker-compose.yml`：tag `0.2` 未固定 digest、`RUSTDESK_API_RUSTDESK_API_SERVER=http://…:21114` 明文、21114/21116 绑 `0.0.0.0`、无资源限制、`MUST_LOGIN`/`RUSTDESK_API_JWT_KEY` 默认注释（默认不强制登录）、`./data/api` 含 bcrypt 与**明文分享密码**。
- 所有镜像 root 运行；`alpine:3` 未装 `tzdata`（`TZ=Asia/Shanghai` 无效）与 `ca-certificates`（OIDC 出站 HTTPS 可能 x509 失败，需实测）。
- `api.Dockerfile`：`npm install` → `npm ci`；构建期 `go mod tidy` 删除；`hbbs/hbbr` 镜像直接用仓库预编译二进制。
- 遗留重复文件：`api/Dockerfile`、`api/docker-compose.yaml`、`api/docker-compose-dev.yaml`；空目录 `api/http/controller/my/`。
- CI（`.github/workflows/ghcr.yml`）只构建推镜像，无 build/vet/test/gofmt；`gofmt -l` 现报 `api/model/oauth.go`、`api/service/ldap.go`。

---

## ✅ 本轮确认无问题

- `api/http/middleware/rustauth.go`：JWT 校验后仍查 DB `UserToken` 并 `CheckUserEnable`，注销/禁用即时生效（设计正确；仅 `token[7:]` 应改 `strings.TrimPrefix` + 前缀校验）。
- `src/jwt.rs` `verify_token`：显式限定 HS256 并手查 `exp`，无算法混淆问题（密钥日志问题见 todo3 R1）。
- `service.UpdateFields` 的“白名单 + 含零值更新”设计本身合理（问题是调用点越权，见 G3）。
- 地址簿共享的权限模型（`UserMaxRule = max(personal, group)`、`CheckUserRead/Write/FullControlPrivilege`）逻辑自洽；`my` 侧规则创建已强制 `UserId = u.Id` 并校验 collection 归属。

---

## 验证记录

```text
$ cd api && go build ./...        # 失败：missing go.sum entry（B1）
$ go test ./lib/cache ./lib/jwt   # setup failed（同上）
$ gofmt -l .                      # api/model/oauth.go, api/service/ldap.go
$ ls api/go.sum                   # No such file or directory
```

## 修复优先级建议

1. **B1**（否则无法验证任何修复）+ CI 接入 build/vet/test。
2. **B2/B3/B4**（未鉴权写入、缓存失效、LDAP 注入）——影响面最大且改动小。
3. **B5/B6/B7/B8**（上传、SSRF、OAuth 状态与绑定）。
4. **G1–G9**（数据正确性与越权，多为一行修复）。
5. **G10–G20**、Rust 与部署项按需排期。

> 本轮仅审查与记录，未改动任何代码；本文件亦未提交。
