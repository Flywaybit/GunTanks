# GunTanks 服务端 Ctrl+C 优雅关闭需求

## 1. 问题

使用 PowerShell 按 `Ctrl+C` 退出服务端后，端口 `8889` 可能暂时无法复用。`netstat -ano | findstr :8889` 可看到服务端处于 `FIN_WAIT_2`，客户端处于 `CLOSE_WAIT`。

原因是 WebSocket 升级连接不由 `http.Server.Shutdown()` 自动管理：服务端先关闭连接，客户端收到 FIN 后没有及时完成 WebSocket/ TCP 关闭；同时客户端断线逻辑可能继续自动重连。

## 2. 目标

- 按 `Ctrl+C` 后停止接受新 HTTP、WebSocket、匹配和战斗请求。
- 已建立的 WebSocket 连接收到标准关闭通知并在限定时间内关闭。
- 客户端收到服务端关闭通知后停止自动重连，并主动关闭 WebSocket。
- 活跃战斗、匹配队列、Redis 在线状态和数据库记录完成清理。
- 正常情况下数秒内释放 `8889`，不再长期出现 `FIN_WAIT_2/CLOSE_WAIT`。
- 关闭流程必须幂等，重复信号不能触发 panic 或重复关闭。

## 3. 服务端要求

### 3.1 连接注册表

在 `guntanks-server/web` 增加 WebSocket 连接管理器：

- 每个升级成功的连接注册唯一 connection ID、用户 ID和底层 `*websocket.Conn`。
- 连接退出时必须从注册表删除。
- 注册表并发安全。
- 服务关闭时可以遍历全部连接并统一关闭。
- 连接关闭操作必须幂等，避免多个 goroutine 重复 `close` 或写入已关闭连接。

### 3.2 关闭顺序

收到 `os.Interrupt` 或 `SIGTERM` 后，严格按以下顺序执行：

1. 设置全局 `shuttingDown=true`，阻止新的登录、WebSocket升级、匹配、房间和战斗命令。
2. 停止客户端自动重连所需的服务端正常业务响应。
3. 从 HTTP 服务移除新请求入口，调用 `http.Server.Shutdown()` 停止监听并等待普通 HTTP 请求完成。
4. 向所有 WebSocket 发送标准关闭帧 `CloseGoingAway`，关闭原因使用 `server shutting down`。
5. 设置 WebSocket 写超时，例如 1 秒；发送关闭帧失败时直接关闭底层连接。
6. 等待 WebSocket handler、读写 goroutine 和连接注册表清空。
7. 停止匹配服务和所有战斗 Actor，将进行中的战斗标记为 `interrupted`，刷新必要记录。
8. 清理本进程持有的 Redis 在线状态、匹配状态和重连状态。
9. 关闭 MongoDB、Redis 等外部资源。
10. 关闭流程达到总超时后，强制关闭剩余 WebSocket、数据库和 Redis 连接，然后退出进程。

推荐总关闭超时为 5 秒，WebSocket 关闭等待时间为 2 秒，所有超时都必须配置化。

### 3.3 WebSocket 关闭实现

每个连接必须执行以下逻辑：

- 先停止向该连接写入普通业务事件。
- 使用 `WriteControl(websocket.CloseMessage, ...)` 发送关闭帧，并设置短截止时间。
- 关闭底层连接，解除阻塞中的 `ReadJSON`。
- 让读协程、写协程和 handler 正常返回。
- 释放该连接对应的 Redis 在线锁；删除操作必须按 `user_id + session_id/generation` 校验，不能删除新登录会话的锁。

不能只依赖 `http.Server.Shutdown()`，也不能只关闭监听 socket；WebSocket 连接必须由连接管理器显式关闭。

### 3.4 handler 生命周期

`web/ws.go` 必须接收服务端关闭 context 或 shutdown channel：

- 连接处理期间监听 shutdown 信号。
- 收到信号后停止读取新业务消息。
- 关闭 outbound 队列前，先确保写协程不会继续向已关闭的连接写数据。
- 不得出现“一个 goroutine 关闭 channel，另一个 goroutine继续写 channel”的 panic。
- handler 的所有 defer 清理逻辑必须只执行一次。

## 4. 客户端要求

修改 `guntanks-client/src/socket.js`：

- 增加 `serverStopping` 或等效状态。
- 收到 WebSocket `CloseGoingAway`、关闭原因包含 `server shutting down` 或服务端停止事件后，设置该状态。
- `serverStopping=true` 时不再执行指数退避自动重连。
- 清理 heartbeat、retry timer、业务发送队列和事件监听。
- 主动调用 `close()` 时必须发送正常 WebSocket close，并等待 `onclose`；超时后强制释放本地引用。
- 服务端关闭期间，客户端页面显示“服务器正在关闭，请稍后重新连接”，不得显示永久加载。
- 服务重新启动后，用户可以手动刷新或重新连接恢复登录。

如果浏览器无法收到 WebSocket 关闭帧，客户端 `onclose` 也必须在关闭原因明确或连接被标记为 stopping 时停止重连；不能无限创建新连接。

## 5. 关闭状态与数据处理

- 新登录、注册和 WebSocket 升级在关闭阶段返回 `SERVICE_UNAVAILABLE`。
- 新匹配请求返回关闭提示，不进入匹配队列。
- 进行中的匹配从队列移除并通知客户端。
- 进行中的战斗统一记录为 `interrupted`，不得结算为某方胜利。
- 客户端断开不应触发服务端等待 60 秒的普通重连淘汰流程；服务端主动关闭时应使用 `server_shutdown` 原因立即结束该流程。
- Redis 在线锁使用会话代次安全清理，避免旧连接关闭时删除新连接的锁。
- 数据库写入设置明确超时，关闭阶段不得无限等待。

## 6. 建议服务端关闭伪代码

```go
func shutdown(ctx context.Context) error {
    state.MarkShuttingDown()
    lobby.StopAccepting()
    httpServer.Shutdown(ctx)

    wsManager.CloseAll(websocket.CloseGoingAway, "server shutting down")
    wsManager.Wait(ctx)

    battles.Shutdown(ctx)
    presence.ReleaseOwnedSessions(ctx)
    store.Close(ctx)
    redis.Close()
    return nil
}
```

实际实现必须保证 `CloseAll` 先发送关闭帧，再关闭底层连接；不能只调用 `httpServer.Shutdown()`。

## 7. 测试与验收

- 启动服务端并打开一个或多个客户端。
- 在 PowerShell 按 `Ctrl+C`。
- 日志显示进入 shutting down、停止接入、关闭 WebSocket、清理战斗和释放资源的顺序。
- 客户端收到关闭提示后不再自动重连。
- 服务端进程在 5 秒内退出。
- 退出后 `netstat -ano | findstr :8889` 不再显示监听端口；正常情况下不残留长期 `FIN_WAIT_2` 或客户端 `CLOSE_WAIT`。
- 关闭后立即重新启动服务端，`8889` 可以成功监听。
- Redis 在线状态、匹配状态和重连状态均被清理。
- 进行中战斗记录为 `interrupted`，不产生错误胜负统计。
- 连续按两次 `Ctrl+C` 不崩溃、不 panic、不重复关闭资源。
- 模拟客户端网络异常时，服务端仍能在关闭超时后强制退出。
- 服务端测试必须覆盖连接注册/注销、关闭帧发送、超时强制关闭、客户端停止重连和重复关闭。

## 8. 禁止事项

- 不得通过延长或缩短 TCP 等待时间掩盖 WebSocket 未关闭问题。
- 不得只调用 `http.Server.Shutdown()` 就认为 WebSocket 已关闭。
- 不得在关闭阶段继续接受新的匹配或战斗命令。
- 不得因为旧连接关闭而删除新登录会话的 Redis 在线锁。
- 不得用进程强杀作为正常 Ctrl+C 关闭流程；强制关闭只能作为超时兜底。
