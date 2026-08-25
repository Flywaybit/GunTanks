# GunTanks 关闭网页后再次登录仍提示 account already online 的修复需求

## 1. 问题描述

用户关闭网页、关闭标签页或刷新后，再次使用同一账号登录时，服务端偶发返回：

`account already online`

这不是游戏逻辑问题，而是账号在线态没有被及时释放，导致 Redis 中的在线锁继续存在，新的登录请求被判定为“已经在线”。

## 2. 根因确认

当前实现里，登录和注册都会先抢占 `online` 锁：

- `guntanks-server/main.go` 中的 `login/register` 调用 `AcquireOnline(...)`
- 如果 Redis 里还保留旧的 `online` 键，就直接返回 `ALREADY_ONLINE`

显式退出是正确的：

- `/api/v1/auth/logout` 会调用 `ReleaseOnline(...)` 和 `ClearReconnect(...)`

真正的问题在 WebSocket 连接关闭后的清理顺序：

- `guntanks-server/web/ws.go` 里，连接退出时先执行 `g.unregister(m)`，再执行 `ReleaseOnline(...)`
- `g.unregister(m)` 会先删除 ownership 记录
- `ReleaseOnline(...)` 又依赖该 ownership 记录做“当前连接是否仍是 owner”的校验
- 结果就是：释放在线锁的条件永远失效，在线锁一直保留到 TTL 自然过期

## 3. 修复目标

必须同时满足以下三点：

1. 用户从大厅/登录态关闭网页后，可以立即重新登录，不再卡在 `ALREADY_ONLINE`
2. 用户在战斗中意外断开后，仍然保留重连能力
3. 显式退出登录仍然立即释放在线锁和重连态

## 4. 必须实现的修复

只修改服务端 WebSocket 收尾逻辑，核心原则是：先决定“要不要释放在线态”，再做 unregister。

### 4.1 修改点

文件：

- `guntanks-server/web/ws.go`

### 4.2 正确的收尾语义

在连接结束时，必须先判断：

- 这是不是当前 owner
- 用户是否仍处于“可重连的进行中战斗”
- 这次断开是不是 `battle.leave` 这类主动离开

然后分支处理：

- 如果是“进行中战斗中的意外断开”：
  - 只写入 `reconnect` 态
  - 不释放 `online` 锁

- 其他所有情况（大厅关闭、页面刷新、非战斗断开、战斗已结束、主动退出后关闭连接）：
  - 立即 `ReleaseOnline(...)`
  - 同时 `ClearReconnect(...)`

最后再执行 `g.unregister(m)`。

### 4.3 关键约束

- `ReleaseOnline(...)` 仍然必须保留 `user + generation/session_id` 校验
- 不允许把 `ReleaseOnline(...)` 单独放成一个会被顺序破坏的 defer
- 不允许用“加大 TTL”掩盖问题
- 不允许把“页面关闭后主动 logout”作为唯一修复，因为这会破坏战斗重连语义

## 5. 不应该做的事

- 不要无条件在每次 WebSocket 断开时立刻释放在线锁
  - 这样会让战斗断线后的重连失效
- 不要只改前端 `beforeunload/pagehide`
  - 这只能覆盖部分场景，不能覆盖网络中断、浏览器崩溃、后台杀进程
- 不要通过延长 Redis TTL 规避
  - 这只是把 bug 变慢，不是修复
- 不要修改登录接口让它忽略 `online` 锁
  - 这会直接破坏“账号不能并发在线”的规则

## 6. 验收标准

以下场景都必须通过：

1. 在大厅关闭网页后，立即重新登录同账号，成功
2. 在战斗中断开后，允许在重连窗口内恢复同一会话
3. 在战斗中主动离开后，再关闭网页，不再残留 `online` 锁
4. 点击显式退出登录后，立即重新登录同账号，成功
5. 新连接建立后，旧连接不能误删新连接的在线锁

## 7. 建议补充的测试

建议增加针对 WebSocket 关闭收尾的单元测试或集成测试，覆盖：

- 大厅断开 -> `ReleaseOnline` 生效
- 战斗中断开 -> 仅写入 `reconnect`
- 主动退出 -> `ReleaseOnline + ClearReconnect`
- 老连接退出 -> 不影响新连接的在线态

## 8. 相关文件

- `guntanks-server/main.go`
- `guntanks-server/web/ws.go`
- `guntanks-server/redis/client.go`
