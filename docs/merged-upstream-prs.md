# 已合并的上游未合并改动

上游 [lejianwen/rustdesk-api](https://github.com/lejianwen/rustdesk-api) 的 issue/PR 队列长期无人处理，本仓库在 fork 时对其未合并的 PR 做了选择性合并。下表记录每个 PR 的取舍与理由，方便后来者对照上游。

## 已合并

| PR | 标题 | 作者 | 内容 | 合并方式 |
|----|------|------|------|----------|
| [#502](https://github.com/lejianwen/rustdesk-api/pull/502) | fix: handle email_verified as JSON string or boolean in OidcUser | kawachi | 部分 OIDC IdP 把 `email_verified` 序列化为字符串 `"true"` 而非布尔值，导致绑定/登录报错。`model.OidcUser` 现在两种都能解析。 | 原样应用（含新增 `model/oauth_test.go`） |
| [#540](https://github.com/lejianwen/rustdesk-api/pull/540) | fix(admin): allow clearing fields on update and require peer id | leonam-daris | 后台「更新」接口用 GORM `Updates(struct)`，零值字段被跳过：设备无法移出分组（group_id=0）、别名/备注无法清空。新增通用 `service.UpdateFields`，peer/user/group/deviceGroup/oauth 五个后台更新接口改为显式字段列表；`request/admin/peer.go` 的 id 字段改为必填。 | 原样应用 |
| [#542](https://github.com/lejianwen/rustdesk-api/pull/542) | fix(config): default captcha-threshold and token-expire when missing | Cody Swartz | 配置缺省时 `captcha-threshold`、token 过期时间落到零值（行为异常）。补齐默认值，含新增 `config/config_test.go`。 | 原样应用 |
| [#543](https://github.com/lejianwen/rustdesk-api/pull/543) | fix(config): read bare token durations as seconds | Cody Swartz | 裸数字的 token 时长配置（如 `expire-duration: 3600`）此前被当作格式错误的 duration 拒绝。现在解析为秒，含新增 `config/duration_test.go`、`service/user_token_test.go`。 | 原样应用 |

验证：`go build ./cmd/apimain.go` + `go test ./config/... ./model/... ./service/...` 全绿（合并于 2026-11-13）。

## 已评估、未合并

| PR | 标题 | 不合并理由 |
|----|------|-----------|
| [#537](https://github.com/lejianwen/rustdesk-api/pull/537) | fix(admin): peer update ignores zero values | 与 #540 修同一个 bug、同一份字段列表；#540 更通用（覆盖全部后台更新接口），已包含 #537 的全部效果，避免两条并存的更新路径 |
| [#541](https://github.com/lejianwen/rustdesk-api/pull/541) | deb 打包保留 conf | 本仓库不发 deb 包 |
| [#536](https://github.com/lejianwen/rustdesk-api/pull/536) | pt-BR 翻译 | 翻译 PR，与维护目标无关，欢迎单独贡献到上游 |
| [#530](https://github.com/lejianwen/rustdesk-api/pull/530) | build 修复 | 针对上游旧 CI 环境，本仓库 CI 已重写 |
| [#445](https://github.com/lejianwen/rustdesk-api/pull/445) | i386 + s6 镜像 | 本仓库镜像方案已重做（三组件 + 多架构），不再适用 |
| [#473](https://github.com/lejianwen/rustdesk-api/pull/473) | （无描述的分支同步） | 无实质内容 |

> 服务端（hbbs/hbbr）侧的「客户端 1.4.7+ 登录后无法连接」修复不来自任何 PR——它来自上游姊妹项目 lejianwen/rustdesk-server 的 `forapi` 分支内置的 key exchange 支持，随本仓库 Rust 源码一并继承。背景见 [DEPLOY.md](../DEPLOY.md) 与 `docs/rustdesk-1.5-clients.md`（api/ 子目录内）。
